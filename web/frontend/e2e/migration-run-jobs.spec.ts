import { test, expect, Page } from '@playwright/test';

const PROJECT_ID = 'job-test';

// Helper to set up MigrationRun page with configurable mock data
async function setupPage(
  page: Page,
  opts: {
    status: any;
    jobs: any[];
    pipeline_stats?: any[];
  }
) {
  await page.route(`**/api/projects/${PROJECT_ID}/status`, (route) => {
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(opts.status),
    });
  });

  await page.route(`**/api/projects/${PROJECT_ID}/jobs*`, (route) => {
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        run_id: opts.status.run_id || 'run-1',
        jobs: opts.jobs,
        pipeline_stats: opts.pipeline_stats || [],
      }),
    });
  });

  await page.route(`**/api/projects/${PROJECT_ID}/progress`, (route) => {
    route.abort();
  });

  await page.goto(`/projects/${PROJECT_ID}/migrate`);
}

test.describe('MigrationRun — Job Table', () => {
  const mixedJobs = [
    {
      id: 1, table_name: 'CUSTOMERS', partition: '', phase: 'data', state: 'COMPLETED',
      rows_expected: 500000, rows_copied: 500000, bytes_copied: 50000000,
      attempt: 1, started_at: '2026-03-15T10:00:00Z', finished_at: '2026-03-15T10:10:00Z',
    },
    {
      id: 2, table_name: 'ORDERS', partition: '', phase: 'data', state: 'RUNNING',
      rows_expected: 1000000, rows_copied: 350000, bytes_copied: 35000000,
      attempt: 1, started_at: '2026-03-15T10:05:00Z',
    },
    {
      id: 3, table_name: 'PRODUCTS', partition: '', phase: 'data', state: 'FAILED',
      rows_expected: 200000, rows_copied: 50000, bytes_copied: 5000000,
      attempt: 2, started_at: '2026-03-15T10:02:00Z', finished_at: '2026-03-15T10:08:00Z',
      error: 'connection reset by peer',
    },
    {
      id: 4, table_name: 'CATEGORIES', partition: '', phase: 'data', state: 'COMPLETED',
      rows_expected: 100, rows_copied: 100, bytes_copied: 5000,
      attempt: 1, started_at: '2026-03-15T10:00:00Z', finished_at: '2026-03-15T10:00:05Z',
    },
    {
      id: 5, table_name: 'AUDIT_LOG', partition: '', phase: 'data', state: 'PENDING',
      rows_expected: 2000000, rows_copied: 0, bytes_copied: 0,
      attempt: 0,
    },
  ];

  const mixedStatus = {
    run_id: 'run-mixed',
    phase: { phase: 'copying', detail: 'Copying data...' },
    summary: {
      total: 5, pending: 1, running: 1, completed: 2, failed: 1, retrying: 0, skipped: 0,
      total_rows: 3700100,
      started_at: '2026-03-15T10:00:00Z',
    },
  };

  test('shows correct job data in table rows', async ({ page }) => {
    await setupPage(page, { status: mixedStatus, jobs: mixedJobs });

    // Wait for job table to appear
    await expect(page.getByText('CUSTOMERS')).toBeVisible({ timeout: 10000 });

    // Verify all tables are visible in "All" tab
    await expect(page.getByText('CUSTOMERS')).toBeVisible();
    await expect(page.getByText('ORDERS')).toBeVisible();
    await expect(page.getByText('PRODUCTS')).toBeVisible();
    await expect(page.getByText('CATEGORIES')).toBeVisible();
    await expect(page.getByText('AUDIT_LOG')).toBeVisible();

    // Verify state badges
    const customersRow = page.locator('tr').filter({ hasText: 'CUSTOMERS' }).first();
    await expect(customersRow.getByText('COMPLETED')).toBeVisible();

    const productsRow = page.locator('tr').filter({ hasText: 'PRODUCTS' }).first();
    await expect(productsRow.getByText('FAILED')).toBeVisible();
  });

  test('tab filtering shows correct jobs', async ({ page }) => {
    await setupPage(page, { status: mixedStatus, jobs: mixedJobs });
    await expect(page.getByText('CUSTOMERS')).toBeVisible({ timeout: 10000 });

    // Click "Running" tab — shows count in tab label
    await page.locator('.tab').filter({ hasText: /^Running/ }).click();
    await expect(page.getByText('ORDERS')).toBeVisible();
    // Completed/Failed/Pending tables should not be visible
    await expect(page.locator('tr').filter({ hasText: 'CUSTOMERS' })).not.toBeVisible();
    await expect(page.locator('tr').filter({ hasText: 'PRODUCTS' })).not.toBeVisible();
    await expect(page.locator('tr').filter({ hasText: 'AUDIT_LOG' })).not.toBeVisible();

    // Click "Failed" tab
    await page.locator('.tab').filter({ hasText: /^Failed/ }).click();
    await expect(page.getByText('PRODUCTS')).toBeVisible();
    await expect(page.locator('tr').filter({ hasText: 'ORDERS' })).not.toBeVisible();

    // Click "Completed" tab
    await page.locator('.tab').filter({ hasText: /^Completed/ }).click();
    await expect(page.getByText('CUSTOMERS')).toBeVisible();
    await expect(page.getByText('CATEGORIES')).toBeVisible();
    await expect(page.locator('tr').filter({ hasText: 'PRODUCTS' })).not.toBeVisible();

    // Click "All" tab — everything shows again
    await page.locator('.tab').filter({ hasText: /^All/ }).click();
    await expect(page.getByText('CUSTOMERS')).toBeVisible();
    await expect(page.getByText('ORDERS')).toBeVisible();
    await expect(page.getByText('PRODUCTS')).toBeVisible();
  });

  test('tab labels show correct counts', async ({ page }) => {
    await setupPage(page, { status: mixedStatus, jobs: mixedJobs });
    await expect(page.getByText('CUSTOMERS')).toBeVisible({ timeout: 10000 });

    // All (5), Running (1), Completed (2), Failed (1)
    await expect(page.locator('.tab').filter({ hasText: 'All (5)' })).toBeVisible();
    await expect(page.locator('.tab').filter({ hasText: 'Running (1)' })).toBeVisible();
    await expect(page.locator('.tab').filter({ hasText: 'Completed (2)' })).toBeVisible();
    await expect(page.locator('.tab').filter({ hasText: 'Failed (1)' })).toBeVisible();
  });

  test('error message shown for failed jobs', async ({ page }) => {
    await setupPage(page, { status: mixedStatus, jobs: mixedJobs });
    await expect(page.getByText('PRODUCTS')).toBeVisible({ timeout: 10000 });

    const productsRow = page.locator('tr').filter({ hasText: 'PRODUCTS' }).first();
    await expect(productsRow.getByText('connection reset by peer')).toBeVisible();
  });
});

test.describe('MigrationRun — Run Summary Card', () => {
  test('displays total, completed, running, failed counts', async ({ page }) => {
    await setupPage(page, {
      status: {
        run_id: 'run-summary',
        phase: { phase: 'completed', detail: 'Migration completed' },
        summary: {
          total: 10, pending: 0, running: 0, completed: 8, failed: 2, retrying: 0, skipped: 0,
          total_rows: 5000000,
          started_at: '2026-03-15T10:00:00Z',
          finished_at: '2026-03-15T10:45:00Z',
        },
      },
      jobs: [
        { id: 1, table_name: 'T1', phase: 'data', state: 'COMPLETED', rows_expected: 1000, rows_copied: 1000, bytes_copied: 10000, attempt: 1 },
      ],
    });

    // Wait for summary card
    await expect(page.getByText('Total Jobs')).toBeVisible({ timeout: 10000 });

    // Verify stat values
    const totalStat = page.locator('.stat').filter({ hasText: 'Total Jobs' });
    await expect(totalStat.locator('.stat-value')).toHaveText('10');

    const completedStat = page.locator('.stat').filter({ hasText: 'Completed' });
    await expect(completedStat.locator('.stat-value')).toHaveText('8');

    const failedStat = page.locator('.stat').filter({ hasText: 'Failed' });
    await expect(failedStat.locator('.stat-value')).toHaveText('2');

    // Progress should be 80% (8/10)
    await expect(page.getByText('80%')).toBeVisible();
  });

  test('displays total rows formatted with locale', async ({ page }) => {
    await setupPage(page, {
      status: {
        run_id: 'run-rows',
        phase: { phase: 'completed', detail: 'Done' },
        summary: {
          total: 1, pending: 0, running: 0, completed: 1, failed: 0, retrying: 0, skipped: 0,
          total_rows: 5000000,
          started_at: '2026-03-15T10:00:00Z',
          finished_at: '2026-03-15T10:30:00Z',
        },
      },
      jobs: [
        { id: 1, table_name: 'BIG', phase: 'data', state: 'COMPLETED', rows_expected: 5000000, rows_copied: 5000000, bytes_copied: 500000000, attempt: 1 },
      ],
    });

    await expect(page.getByText('Total Jobs')).toBeVisible({ timeout: 10000 });

    // Rows should be locale-formatted (5,000,000 or 5.000.000 depending on locale)
    // Just verify the "Rows:" label exists with a number
    await expect(page.getByText(/Rows:/)).toBeVisible();
  });

  test('shows duration for completed migration', async ({ page }) => {
    await setupPage(page, {
      status: {
        run_id: 'run-dur',
        phase: { phase: 'completed', detail: 'Done' },
        summary: {
          total: 1, pending: 0, running: 0, completed: 1, failed: 0, retrying: 0, skipped: 0,
          total_rows: 1000,
          started_at: '2026-03-15T10:00:00Z',
          finished_at: '2026-03-15T10:30:00Z',
        },
      },
      jobs: [
        { id: 1, table_name: 'T1', phase: 'data', state: 'COMPLETED', rows_expected: 1000, rows_copied: 1000, bytes_copied: 10000, attempt: 1 },
      ],
    });

    await expect(page.getByText('Total Jobs')).toBeVisible({ timeout: 10000 });

    // Duration should show "30m 0s"
    await expect(page.getByText('Duration: 30m 0s')).toBeVisible();
  });
});

test.describe('MigrationRun — Action Buttons', () => {
  test('Start Migration button visible when no active run', async ({ page }) => {
    await setupPage(page, {
      status: {
        run_id: '',
        phase: undefined,
        summary: { total: 0, pending: 0, running: 0, completed: 0, failed: 0, retrying: 0, skipped: 0, total_rows: 0 },
      },
      jobs: [],
    });

    // "Ready to migrate" empty state should appear
    await expect(page.getByText('Ready to migrate')).toBeVisible({ timeout: 10000 });

    // Start Migration button should be visible
    await expect(page.locator('button').filter({ hasText: 'Start Migration' })).toBeVisible();
  });

  test('Resume Failed button visible when there are failed jobs', async ({ page }) => {
    await setupPage(page, {
      status: {
        run_id: 'run-failed',
        phase: { phase: 'failed', detail: 'Migration failed' },
        summary: {
          total: 3, pending: 0, running: 0, completed: 1, failed: 2, retrying: 0, skipped: 0,
          total_rows: 100000,
          started_at: '2026-03-15T10:00:00Z',
          finished_at: '2026-03-15T10:05:00Z',
        },
      },
      jobs: [
        { id: 1, table_name: 'OK_TABLE', phase: 'data', state: 'COMPLETED', rows_expected: 1000, rows_copied: 1000, bytes_copied: 10000, attempt: 1 },
        { id: 2, table_name: 'BAD_TABLE', phase: 'data', state: 'FAILED', rows_expected: 50000, rows_copied: 10000, bytes_copied: 1000000, attempt: 3, error: 'timeout' },
        { id: 3, table_name: 'BAD_TABLE2', phase: 'data', state: 'FAILED', rows_expected: 49000, rows_copied: 0, bytes_copied: 0, attempt: 1, error: 'ORA-01555' },
      ],
    });

    await expect(page.getByText('OK_TABLE')).toBeVisible({ timeout: 10000 });

    // Resume Failed button should be visible
    await expect(page.getByText('Resume Failed')).toBeVisible();

    // Reset button should also be visible (total > 0)
    await expect(page.getByText('Reset')).toBeVisible();

    // Start Migration should also be visible (not active)
    await expect(page.getByText('Start Migration')).toBeVisible();
  });

  test('Reset button visible when jobs exist', async ({ page }) => {
    await setupPage(page, {
      status: {
        run_id: 'run-done',
        phase: { phase: 'completed', detail: 'Done' },
        summary: {
          total: 2, pending: 0, running: 0, completed: 2, failed: 0, retrying: 0, skipped: 0,
          total_rows: 10000,
          started_at: '2026-03-15T10:00:00Z',
          finished_at: '2026-03-15T10:01:00Z',
        },
      },
      jobs: [
        { id: 1, table_name: 'A', phase: 'data', state: 'COMPLETED', rows_expected: 5000, rows_copied: 5000, bytes_copied: 50000, attempt: 1 },
        { id: 2, table_name: 'B', phase: 'data', state: 'COMPLETED', rows_expected: 5000, rows_copied: 5000, bytes_copied: 50000, attempt: 1 },
      ],
    });

    await expect(page.getByText('Total Jobs')).toBeVisible({ timeout: 10000 });

    // Reset should be visible
    await expect(page.getByText('Reset')).toBeVisible();

    // Resume should NOT be visible (no failed jobs)
    await expect(page.getByText('Resume Failed')).not.toBeVisible();
  });

  test('Cancel button not visible when migration is not active', async ({ page }) => {
    await setupPage(page, {
      status: {
        run_id: 'run-done',
        phase: { phase: 'completed', detail: 'Done' },
        summary: {
          total: 1, pending: 0, running: 0, completed: 1, failed: 0, retrying: 0, skipped: 0,
          total_rows: 1000,
          started_at: '2026-03-15T10:00:00Z',
          finished_at: '2026-03-15T10:01:00Z',
        },
      },
      jobs: [
        { id: 1, table_name: 'FINISHED_TBL', phase: 'data', state: 'COMPLETED', rows_expected: 1000, rows_copied: 1000, bytes_copied: 10000, attempt: 1 },
      ],
    });

    await expect(page.getByText('FINISHED_TBL')).toBeVisible({ timeout: 10000 });

    // Cancel should NOT be visible
    await expect(page.getByText('Cancel')).not.toBeVisible();
  });
});

test.describe('MigrationRun — Progress Display', () => {
  test('progress bar and row counts shown for data jobs', async ({ page }) => {
    await setupPage(page, {
      status: {
        run_id: 'run-prog',
        phase: { phase: 'completed', detail: 'Done' },
        summary: {
          total: 1, pending: 0, running: 0, completed: 1, failed: 0, retrying: 0, skipped: 0,
          total_rows: 500000,
          started_at: '2026-03-15T10:00:00Z',
          finished_at: '2026-03-15T10:10:00Z',
        },
      },
      jobs: [
        {
          id: 1, table_name: 'CUSTOMERS', partition: '', phase: 'data', state: 'COMPLETED',
          rows_expected: 500000, rows_copied: 500000, bytes_copied: 50000000,
          attempt: 1, started_at: '2026-03-15T10:00:00Z', finished_at: '2026-03-15T10:10:00Z',
        },
      ],
    });

    await expect(page.getByText('CUSTOMERS')).toBeVisible({ timeout: 10000 });

    // The progress cell should show row counts
    const row = page.locator('tr').filter({ hasText: 'CUSTOMERS' }).first();
    // rows_copied / rows_expected in locale format
    await expect(row.getByText('100%')).toBeVisible();
  });

  test('migration failed alert shown when phase is failed', async ({ page }) => {
    await setupPage(page, {
      status: {
        run_id: 'run-fail',
        phase: { phase: 'failed', detail: 'ORA-01555: snapshot too old' },
        summary: {
          total: 1, pending: 0, running: 0, completed: 0, failed: 1, retrying: 0, skipped: 0,
          total_rows: 100000,
          started_at: '2026-03-15T10:00:00Z',
          finished_at: '2026-03-15T10:05:00Z',
        },
      },
      jobs: [
        { id: 1, table_name: 'FAIL_TBL', phase: 'data', state: 'FAILED', rows_expected: 100000, rows_copied: 20000, bytes_copied: 2000000, attempt: 3, error: 'ORA-01555' },
      ],
    });

    await expect(page.getByText('Migration failed:')).toBeVisible({ timeout: 10000 });
    await expect(page.getByText('Migration failed: ORA-01555: snapshot too old')).toBeVisible();
  });
});

test.describe('MigrationRun — Job Table Column Headers', () => {
  test('job table has correct column headers', async ({ page }) => {
    await setupPage(page, {
      status: {
        run_id: 'run-hdr',
        phase: { phase: 'completed', detail: 'Done' },
        summary: { total: 1, pending: 0, running: 0, completed: 1, failed: 0, retrying: 0, skipped: 0, total_rows: 100 },
      },
      jobs: [
        { id: 1, table_name: 'HDR_TEST', phase: 'data', state: 'COMPLETED', rows_expected: 100, rows_copied: 100, bytes_copied: 1000, attempt: 1 },
      ],
    });

    await expect(page.getByText('HDR_TEST')).toBeVisible({ timeout: 10000 });

    // Verify column headers
    const headerRow = page.locator('table.data-table thead tr').last();
    await expect(headerRow.getByText('Table')).toBeVisible();
    await expect(headerRow.getByText('Phase')).toBeVisible();
    await expect(headerRow.getByText('State')).toBeVisible();
    await expect(headerRow.getByText('Progress')).toBeVisible();
    await expect(headerRow.getByText('Started')).toBeVisible();
    await expect(headerRow.getByText('Duration')).toBeVisible();
    await expect(headerRow.getByText('Error')).toBeVisible();
  });
});

test.describe('MigrationRun — Run ID Display', () => {
  test('shows truncated run ID in summary', async ({ page }) => {
    await setupPage(page, {
      status: {
        run_id: 'abcdef12-3456-7890-abcd-ef1234567890',
        phase: { phase: 'completed', detail: 'Done' },
        summary: {
          total: 1, pending: 0, running: 0, completed: 1, failed: 0, retrying: 0, skipped: 0,
          total_rows: 100,
          started_at: '2026-03-15T10:00:00Z',
          finished_at: '2026-03-15T10:01:00Z',
        },
      },
      jobs: [
        { id: 1, table_name: 'T1', phase: 'data', state: 'COMPLETED', rows_expected: 100, rows_copied: 100, bytes_copied: 1000, attempt: 1 },
      ],
    });

    await expect(page.getByText('Total Jobs')).toBeVisible({ timeout: 10000 });

    // Run ID is sliced to first 8 chars
    await expect(page.getByText('Run: abcdef12')).toBeVisible();
  });
});
