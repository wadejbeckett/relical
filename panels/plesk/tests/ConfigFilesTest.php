<?php

declare(strict_types=1);

namespace Noiz\Relical\Tests;

use Noiz\Relical\ConfigFiles;
use Noiz\Relical\ConfigWriter;
use PHPUnit\Framework\TestCase;

final class ConfigFilesTest extends TestCase
{
    private string $dir;

    protected function setUp(): void
    {
        $this->dir = sys_get_temp_dir() . '/relical-cf-' . bin2hex(random_bytes(4));
        mkdir($this->dir, 0700);
    }

    protected function tearDown(): void
    {
        foreach (glob($this->dir . '/{,.}*', GLOB_BRACE) ?: [] as $file) {
            if (is_file($file) || is_link($file)) {
                unlink($file);
            }
        }
        rmdir($this->dir);
    }

    public function testTheOptionFileFollowsAChangedPassword(): void
    {
        $shadow = $this->dir . '/shadow';
        $cnf = $this->dir . '/mysql.cnf';
        file_put_contents($shadow, "first-pass\n");

        self::assertTrue(ConfigFiles::refreshMysqlOptionFile($shadow, $cnf), 'missing: written');
        self::assertSame(ConfigWriter::mysqlOptionFile('first-pass'), file_get_contents($cnf));
        self::assertSame(0600, fileperms($cnf) & 0777);
        self::assertFalse(ConfigFiles::refreshMysqlOptionFile($shadow, $cnf), 'unchanged: left alone');

        file_put_contents($shadow, "second-pass\n");
        self::assertTrue(ConfigFiles::refreshMysqlOptionFile($shadow, $cnf));
        self::assertSame(ConfigWriter::mysqlOptionFile('second-pass'), file_get_contents($cnf));
    }

    public function testLoosePermissionsArePutBackWhenThePasswordIsRight(): void
    {
        $shadow = $this->dir . '/shadow';
        $cnf = $this->dir . '/mysql.cnf';
        file_put_contents($shadow, "first-pass\n");
        file_put_contents($cnf, ConfigWriter::mysqlOptionFile('first-pass'));
        chmod($cnf, 0644);

        self::assertTrue(ConfigFiles::refreshMysqlOptionFile($shadow, $cnf), 'a world-readable file is rewritten');
        clearstatcache();
        self::assertSame(0600, fileperms($cnf) & 07777);
        self::assertSame(ConfigWriter::mysqlOptionFile('first-pass'), file_get_contents($cnf));
        self::assertSame([], glob($this->dir . '/.*.tmp') ?: [], 'no temporary file is left');
        self::assertFalse(ConfigFiles::refreshMysqlOptionFile($shadow, $cnf), 'then it is left alone');
    }

    public function testAFileOwnedBySomeoneElseIsRewritten(): void
    {
        $shadow = $this->dir . '/shadow';
        $cnf = $this->dir . '/mysql.cnf';
        file_put_contents($shadow, "first-pass\n");
        self::assertTrue(ConfigFiles::refreshMysqlOptionFile($shadow, $cnf));
        $mine = (int) fileowner($cnf);

        self::assertFalse(ConfigFiles::refreshMysqlOptionFile($shadow, $cnf, $mine));
        self::assertTrue(ConfigFiles::refreshMysqlOptionFile($shadow, $cnf, $mine + 1), 'not owned by the expected user: rewritten');
    }

    public function testAnUnreadableShadowOrALinkedTargetIsAnError(): void
    {
        try {
            ConfigFiles::refreshMysqlOptionFile($this->dir . '/none', $this->dir . '/mysql.cnf');
            self::fail('a missing shadow was accepted');
        } catch (\RuntimeException $e) {
            self::assertStringContainsString('could not read', $e->getMessage());
        }
        file_put_contents($this->dir . '/shadow', 'secret-pass');
        symlink($this->dir . '/elsewhere', $this->dir . '/mysql.cnf');
        $this->expectException(\RuntimeException::class);
        $this->expectExceptionMessage('symbolic link');
        ConfigFiles::refreshMysqlOptionFile($this->dir . '/shadow', $this->dir . '/mysql.cnf');
    }

    public function testThePasswordIsNeverInAMessage(): void
    {
        file_put_contents($this->dir . '/shadow', 'secret-pass');
        symlink($this->dir . '/elsewhere', $this->dir . '/mysql.cnf');
        try {
            ConfigFiles::refreshMysqlOptionFile($this->dir . '/shadow', $this->dir . '/mysql.cnf');
        } catch (\RuntimeException $e) {
            self::assertStringNotContainsString('secret-pass', $e->getMessage());
        }
    }

    public function testInventoryRefreshesTheOptionFileEveryRun(): void
    {
        $script = (string) file_get_contents(__DIR__ . '/../plib/scripts/inventory.php');

        self::assertStringContainsString('ConfigFiles::refreshMysqlOptionFile()', $script);
    }
}
