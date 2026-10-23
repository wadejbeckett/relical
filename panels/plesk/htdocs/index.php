<?php

declare(strict_types=1);

/**
 * Web entry point for the Relical Backup extension.
 *
 * Plesk routes /modules/relical/ to this file; pm_Application dispatches the
 * request to the controllers in plib/controllers/.
 */

pm_Context::init('relical');

$application = new pm_Application();
$application->run();
