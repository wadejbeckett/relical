<?php

declare(strict_types=1);

namespace Noiz\Relical;

use RuntimeException;
use Throwable;

/**
 * Carries out an Installer plan, printing one line per step.
 *
 * A failed step stops the run with an exception, except an optional one,
 * which is reported as a warning while the remaining steps still run. The
 * exception names the steps that were completed before the failure and the
 * ones that were not, and how to run the whole plan again, so the
 * administrator knows what state the server is in.
 */
final class InstallRunner
{
    /** @var list<string> */
    private array $warnings = [];

    /**
     * @param resource $out
     * @param resource $err
     */
    public function __construct(private $out, private $err, private string $prefix = 'Relical: ')
    {
    }

    /**
     * @param list<Action> $plan
     * @param string       $rerun how to run the plan again, added to a failure's message
     */
    public function run(array $plan, string $rerun = ''): void
    {
        $done = [];
        foreach (array_values($plan) as $index => $action) {
            fwrite($this->out, $this->prefix . $action->description . "\n");
            try {
                $this->apply($action);
                if ($action->type !== Action::NOTE) {
                    $done[] = $action->description;
                }
            } catch (Throwable $e) {
                if (!$action->optional) {
                    throw new RuntimeException(self::failure($action, $e, $done, array_slice($plan, $index + 1), $rerun), 0, $e);
                }
                $warning = sprintf('warning: %s failed: %s', $action->description, $e->getMessage());
                $this->warnings[] = $warning;
                fwrite($this->err, $this->prefix . $warning . "\n");
            }
        }
    }

    /** @return list<string> the optional steps that failed */
    public function warnings(): array
    {
        return $this->warnings;
    }

    /**
     * @param list<string> $done descriptions of the completed steps
     * @param list<Action> $rest the steps after the failed one
     */
    private static function failure(Action $failed, Throwable $e, array $done, array $rest, string $rerun): string
    {
        $notDone = [$failed->description];
        foreach ($rest as $action) {
            if ($action->type !== Action::NOTE) {
                $notDone[] = $action->description;
            }
        }
        $message = sprintf('%s failed: %s.', $failed->description, $e->getMessage());
        $message .= $done === [] ? ' Nothing was changed.' : ' Completed: ' . implode('; ', $done) . '.';
        $message .= ' Not done: ' . implode('; ', $notDone) . '.';
        if ($rerun !== '') {
            $message .= ' ' . $rerun;
        }

        return $message;
    }

    private function apply(Action $action): void
    {
        $p = $action->params;
        switch ($action->type) {
            case Action::NOTE:
                return;
            case Action::ENSURE_DIR:
                RootFile::ensureDir($p['path'], $p['mode']);
                if (self::isRoot() && !(@chown($p['path'], 0) && @chgrp($p['path'], 0))) {
                    throw new RuntimeException(sprintf('could not give %s to root', $p['path']));
                }
                return;
            case Action::INSTALL_BINARY:
                RootFile::install($p['from'], $p['path'], $p['mode'], $p['sha256']);
                return;
            case Action::WRITE_FILE:
                RootFile::write($p['path'], $p['content'], $p['mode']);
                return;
            case Action::WRITE_CONFIG:
                if ($p['with_destination']) {
                    ConfigFiles::write($p['input'], $p['server_id']);
                } else {
                    ConfigFiles::writeMysqlOptionFile();
                }
                return;
            case Action::RUN:
                $code = Process::run($p['argv'], $this->out, $this->err);
                if ($code !== 0) {
                    throw new RuntimeException(sprintf('exit %d', $code));
                }
                return;
            case Action::REMOVE_FILE:
                if ((file_exists($p['path']) || is_link($p['path'])) && !@unlink($p['path'])) {
                    throw new RuntimeException(sprintf('could not remove %s', $p['path']));
                }
                return;
            case Action::RENAME:
                if (is_link($p['path'])) {
                    throw new RuntimeException(sprintf('%s is a symbolic link', $p['path']));
                }
                if (!@rename($p['from'], $p['path'])) {
                    throw new RuntimeException(sprintf('could not rename %s to %s', $p['from'], $p['path']));
                }
                return;
            case Action::REMOVE_DIR:
                if (is_link($p['path'])) {
                    throw new RuntimeException(sprintf('%s is a symbolic link', $p['path']));
                }
                if (is_dir($p['path']) && !@rmdir($p['path'])) {
                    throw new RuntimeException(sprintf('could not remove %s (is it empty?)', $p['path']));
                }
                return;
        }

        throw new RuntimeException(sprintf('unknown step %s', $action->type));
    }

    private static function isRoot(): bool
    {
        return function_exists('posix_geteuid') && posix_geteuid() === 0;
    }
}
