<?php

declare(strict_types=1);

/**
 * Reports a failed backup or check. systemd runs it as root through
 * relical-notify@.service, which both services name in OnFailure=:
 *
 *     relical-root notify.php relical-backup.service
 *
 * Appends a line to the run log, keeps the failure in
 * /var/lib/relical/last-failure.json for the settings page, and mails the
 * Plesk administrator (Notify explains where the address comes from).
 * Without Plesk's SDK it logs only. Prints {"logged", "mailed", "to"}.
 */

use Noiz\Relical\LogTail;
use Noiz\Relical\Notify;
use Noiz\Relical\Paths;
use Noiz\Relical\RootFile;
use Noiz\Relical\ScriptIO;

// Plesk SDK and module context: see the note in cli.php.
if (class_exists('pm_Loader')) {
    pm_Loader::registerAutoload();
}
if (class_exists('pm_Context')) {
    pm_Context::init('relical');
}

require_once __DIR__ . '/../library/autoload.php';

$args = array_slice($argv, 1);
try {
    if (count($args) !== 1) {
        throw new InvalidArgumentException('notify.php takes <unit>');
    }
    $unit = Notify::unit((string) $args[0]);
} catch (InvalidArgumentException $e) {
    ScriptIO::fail('refused: ' . $e->getMessage(), 2);
}
ScriptIO::requireRoot('notify.php');

$now = time();
$out = ['logged' => false, 'mailed' => false, 'to' => ''];
$problems = [];

try {
    RootFile::ensureDir(Paths::STATE_DIR);
    if (is_link(Paths::LOG)) {
        throw new RuntimeException(Paths::LOG . ' is a symbolic link');
    }
    $out['logged'] = @file_put_contents(Paths::LOG, Notify::logLine($unit, $now), FILE_APPEND | LOCK_EX) !== false;
    RootFile::write(Paths::LAST_FAILURE, json_encode(Notify::record($unit, $now), JSON_UNESCAPED_SLASHES | JSON_THROW_ON_ERROR) . "\n");
} catch (Throwable $e) {
    $problems[] = $e->getMessage();
}

$to = '';
try {
    if (class_exists('pm_ApiRpc')) {
        $response = pm_ApiRpc::getService()->call(Notify::ADMIN_REQUEST);
        $to = Notify::adminEmail($response instanceof SimpleXMLElement ? $response : null);
    }
    if ($to === '' && class_exists('pm_Client')) {
        $to = Notify::validEmail((string) pm_Client::getByLogin('admin')->getProperty('email'));
    }
} catch (Throwable $e) {
    $problems[] = 'the administrator address could not be read: ' . $e->getMessage();
}

if ($to !== '') {
    try {
        $tail = LogTail::read(Paths::LOG, Notify::MAIL_LOG_LINES) ?? [];
    } catch (Throwable $e) {
        $tail = [];
    }
    $mail = Notify::message($unit, (string) gethostname(), $now, $tail);
    $out['to'] = $to;
    $out['mailed'] = mail($to, $mail['subject'], $mail['body'], ['Content-Type' => 'text/plain; charset=UTF-8']);
    if (!$out['mailed']) {
        $problems[] = 'mail() refused the message';
    }
} else {
    $problems[] = 'no administrator address; logged only';
}

foreach ($problems as $problem) {
    fwrite(STDERR, 'notify: ' . $problem . "\n");
}
ScriptIO::ok($out);
