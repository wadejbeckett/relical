<?php

declare(strict_types=1);

/**
 * The extension's only way to run the core binary.
 *
 * Run as root through `plesk bin extension --exec relical cli.php <command> [flags]`.
 * Relical\Command accepts only the commands and flags of the CLI contract
 * and validates every value; the core then runs from an argv array (no
 * shell), its JSON goes straight to stdout and its exit code is this
 * script's exit code. A refused command prints `{"error": "..."}` and exits 2.
 */

use Noiz\Relical\Command;
use Noiz\Relical\Process;

pm_Loader::registerAutoload();
pm_Context::init('relical');

require_once __DIR__ . '/../library/autoload.php';

try {
    $command = Command::parse(array_slice($argv, 1));
} catch (InvalidArgumentException $e) {
    echo json_encode(['error' => 'refused: ' . $e->getMessage()], JSON_UNESCAPED_SLASHES | JSON_INVALID_UTF8_SUBSTITUTE), "\n";
    exit(2);
}

try {
    exit(Process::run($command->argv(), STDOUT, STDERR));
} catch (Throwable $e) {
    echo json_encode(['error' => $e->getMessage()], JSON_UNESCAPED_SLASHES | JSON_INVALID_UTF8_SUBSTITUTE), "\n";
    exit(1);
}
