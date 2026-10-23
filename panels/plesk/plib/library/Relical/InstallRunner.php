<?php

declare(strict_types=1);

namespace Noiz\Relical;

use RuntimeException;
use Throwable;

/**
 * Carries out an Installer plan, printing one line per step.
 *
 * A failed step stops the run with an exception, except an optional one,
 * which is reported as a warning while the remaining steps still run.
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

    /** @param list<Action> $plan */
    public function run(array $plan): void
    {
        foreach ($plan as $action) {
            fwrite($this->out, $this->prefix . $action->description . "\n");
            try {
                $this->apply($action);
            } catch (Throwable $e) {
                if (!$action->optional) {
                    throw new RuntimeException(sprintf('%s failed: %s', $action->description, $e->getMessage()), 0, $e);
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
