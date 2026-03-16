import { test, expect, Page } from '@playwright/test';

// Helper to set up MigrationRun page with mock data
async function setupMigrationPage(page: Page, projectId: string = 'test-proj') {
  // Mock ALL API calls that might be triggered
  await page.route(`**/api/projects/${projectId}/status`, (route) => {
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        run_id: 'run-1',
        phase: { phase: 'completed', detail: 'Migration completed' },
        summary: {
          total: 2, pending: 0, running: 0, completed: 2, failed: 0, retrying: 0, skipped: 0,
          total_rows: 1500000,
          started_at: '2026-03-15T10:00:00Z',
          finished_at: '2026-03-15T10:30:00Z',
        },
      }),
    });
  });

  await page.route(`**/api/projects/${projectId}/jobs*`, (route) => {
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        run_id: 'run-1',
        jobs: [
          {
            id: 1, table_name: 'CUSTOMERS', phase: 'data', state: 'completed',
            rows_expected: 500000, rows_copied: 500000, bytes_copied: 50000000,
            attempt: 1, started_at: '2026-03-15T10:00:00Z', finished_at: '2026-03-15T10:10:00Z',
          },
          {
            id: 2, table_name: 'ORDERS', partition: 'P202401', phase: 'data', state: 'completed',
            rows_expected: 1000000, rows_copied: 1000000, bytes_copied: 120000000,
            attempt: 1, started_at: '2026-03-15T10:00:00Z', finished_at: '2026-03-15T10:25:00Z',
          },
        ],
        pipeline_stats: [
          {
            table: 'CUSTOMERS', partition: '', rows: 500000, speed: 83000, percent: 100,
            state: 'STATS',
            read_time_ms: 1800, write_time_ms: 4200,
            read_pct: 30, write_pct: 70, batches: 10, avg_batch_ms: 420,
            duration_ms: 6000,
            acquire_wait_ms: 50,
            commit_wait_ms: 800,
          },
          {
            table: 'ORDERS', partition: 'P202401', rows: 1000000, speed: 66000, percent: 100,
            state: 'STATS',
            read_time_ms: 4500, write_time_ms: 10500,
            read_pct: 30, write_pct: 70, batches: 20, avg_batch_ms: 525,
            duration_ms: 15000,
            acquire_wait_ms: 2500,   // 2500/15000 = 16.7% > 10% → pool contention
            commit_wait_ms: 3000,
          },
        ],
      }),
    });
  });

  // SSE endpoint — return empty response to avoid hanging connection
  await page.route(`**/api/projects/${projectId}/progress`, (route) => {
    route.abort();
  });
}

test.describe('Pipeline Analytics — Bottleneck Details', () => {
  test('shows PG Write bottleneck with batch/commit/pool breakdown', async ({ page }) => {
    await setupMigrationPage(page);
    await page.goto(`/projects/test-proj/migrate`);

    // Wait for pipeline analytics section
    await expect(page.getByText('Pipeline Analytics')).toBeVisible({ timeout: 10000 });

    // CUSTOMERS row should show PG Write badge (write_pct > read_pct)
    const customersRow = page.locator('tr').filter({ hasText: 'CUSTOMERS' }).first();
    await expect(customersRow.getByText('PG Write')).toBeVisible();

    // Should show detailed breakdown for PG Write bottleneck
    await expect(customersRow.getByText(/batch:/)).toBeVisible();
    await expect(customersRow.getByText(/commit:/)).toBeVisible();
    await expect(customersRow.getByText(/pool:/)).toBeVisible();
  });

  test('shows pool contention warning when acquire_wait > 10% of duration', async ({ page }) => {
    await setupMigrationPage(page);
    await page.goto('/projects/test-proj/migrate');

    await expect(page.getByText('Pipeline Analytics')).toBeVisible({ timeout: 10000 });

    // ORDERS has acquire_wait_ms=2500, duration_ms=15000 → 16.7% > 10% threshold
    const ordersRow = page.locator('tr').filter({ hasText: 'ORDERS' }).first();
    await expect(ordersRow.getByText('Pool contention')).toBeVisible();
  });

  test('does NOT show pool contention for CUSTOMERS (low acquire wait)', async ({ page }) => {
    await setupMigrationPage(page);
    await page.goto('/projects/test-proj/migrate');

    await expect(page.getByText('Pipeline Analytics')).toBeVisible({ timeout: 10000 });

    // CUSTOMERS has acquire_wait_ms=50, duration_ms=6000000 → way below 10%
    const customersRow = page.locator('tr').filter({ hasText: 'CUSTOMERS' }).first();
    await expect(customersRow.getByText('Pool contention')).not.toBeVisible();
  });

  test('shows Oracle Read bottleneck when read_pct > write_pct', async ({ page }) => {
    const projectId = 'read-heavy';

    await page.route(`**/api/projects/${projectId}/status`, (route) => {
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          run_id: 'run-1',
          phase: { phase: 'completed', detail: 'Done' },
          summary: { total: 1, completed: 1, pending: 0, running: 0, failed: 0, retrying: 0, skipped: 0, total_rows: 100000 },
        }),
      });
    });

    await page.route(`**/api/projects/${projectId}/jobs*`, (route) => {
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          run_id: 'run-1',
          jobs: [{
            id: 1, table_name: 'SLOW_SOURCE', phase: 'data', state: 'completed',
            rows_expected: 100000, rows_copied: 100000, bytes_copied: 10000000,
            attempt: 1,
          }],
          pipeline_stats: [{
            table: 'SLOW_SOURCE', partition: '', rows: 100000, speed: 5000, percent: 100,
            state: 'STATS',
            read_time_ms: 14000, write_time_ms: 6000,
            read_pct: 70, write_pct: 30, batches: 2, avg_batch_ms: 3000,
            duration_ms: 20000,
            acquire_wait_ms: 10,
            commit_wait_ms: 50,
          }],
        }),
      });
    });

    await page.route(`**/api/projects/${projectId}/progress`, (route) => {
      route.abort();
    });

    await page.goto(`/projects/${projectId}/migrate`);
    await expect(page.getByText('Pipeline Analytics')).toBeVisible({ timeout: 10000 });

    const row = page.locator('tr').filter({ hasText: 'SLOW_SOURCE' }).first();
    await expect(row.getByText('Oracle Read')).toBeVisible();
    // Should NOT show batch/commit/pool breakdown for Oracle Read bottleneck
    await expect(row.getByText(/batch:/)).not.toBeVisible();
  });

  test('aggregate summary shows primary bottleneck', async ({ page }) => {
    await setupMigrationPage(page);
    await page.goto('/projects/test-proj/migrate');

    await expect(page.getByText('Pipeline Analytics')).toBeVisible({ timeout: 10000 });

    // Both tables are PG Write bottleneck → aggregate should say PG Write
    await expect(page.getByText(/Primary bottleneck:/)).toBeVisible();
    await expect(page.getByText('Primary bottleneck: PG Write')).toBeVisible();
  });
});

test.describe('Pipeline Analytics — Multi-Partition Expansion', () => {
  test('expand table with 3 partitions shows child rows', async ({ page }) => {
    const projectId = 'multi-part';

    await page.route(`**/api/projects/${projectId}/status`, (route) => {
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          run_id: 'run-1',
          phase: { phase: 'completed', detail: 'Done' },
          summary: { total: 3, completed: 3, pending: 0, running: 0, failed: 0, retrying: 0, skipped: 0, total_rows: 3000000 },
        }),
      });
    });

    await page.route(`**/api/projects/${projectId}/jobs*`, (route) => {
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          run_id: 'run-1',
          jobs: [
            { id: 1, table_name: 'SALES', partition: 'P202401', phase: 'data', state: 'COMPLETED', rows_expected: 1000000, rows_copied: 1000000, bytes_copied: 100000000, attempt: 1 },
            { id: 2, table_name: 'SALES', partition: 'P202402', phase: 'data', state: 'COMPLETED', rows_expected: 1200000, rows_copied: 1200000, bytes_copied: 120000000, attempt: 1 },
            { id: 3, table_name: 'SALES', partition: 'P202403', phase: 'data', state: 'COMPLETED', rows_expected: 800000, rows_copied: 800000, bytes_copied: 80000000, attempt: 1 },
          ],
          pipeline_stats: [
            { table: 'SALES', partition: 'P202401', rows: 1000000, speed: 50000, percent: 100, state: 'STATS', read_time_ms: 3000, write_time_ms: 7000, read_pct: 30, write_pct: 70, batches: 20, avg_batch_ms: 350, duration_ms: 10000, acquire_wait_ms: 100, commit_wait_ms: 500 },
            { table: 'SALES', partition: 'P202402', rows: 1200000, speed: 60000, percent: 100, state: 'STATS', read_time_ms: 3600, write_time_ms: 8400, read_pct: 30, write_pct: 70, batches: 24, avg_batch_ms: 350, duration_ms: 12000, acquire_wait_ms: 120, commit_wait_ms: 600 },
            { table: 'SALES', partition: 'P202403', rows: 800000, speed: 40000, percent: 100, state: 'STATS', read_time_ms: 2400, write_time_ms: 5600, read_pct: 30, write_pct: 70, batches: 16, avg_batch_ms: 350, duration_ms: 8000, acquire_wait_ms: 80, commit_wait_ms: 400 },
          ],
        }),
      });
    });

    await page.route(`**/api/projects/${projectId}/progress`, (route) => {
      route.abort();
    });

    await page.goto(`/projects/${projectId}/migrate`);
    await expect(page.getByText('Pipeline Analytics')).toBeVisible({ timeout: 10000 });

    // Parent row should show "3 tasks" detail
    const salesRow = page.locator('tr').filter({ hasText: 'SALES' }).first();
    await expect(salesRow.getByText('3 tasks')).toBeVisible();

    // Click to expand
    await salesRow.click();

    // Child rows should appear with partition names
    await expect(page.getByText('P202401')).toBeVisible();
    await expect(page.getByText('P202402')).toBeVisible();
    await expect(page.getByText('P202403')).toBeVisible();
  });
});

test.describe('Pipeline Analytics — Duration Formatting', () => {
  test('fmtMs formats correctly for milliseconds, seconds, and minutes', async ({ page }) => {
    const projectId = 'fmt-test';

    await page.route(`**/api/projects/${projectId}/status`, (route) => {
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          run_id: 'run-1',
          phase: { phase: 'completed', detail: 'Done' },
          summary: { total: 3, completed: 3, pending: 0, running: 0, failed: 0, retrying: 0, skipped: 0, total_rows: 300000 },
        }),
      });
    });

    await page.route(`**/api/projects/${projectId}/jobs*`, (route) => {
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          run_id: 'run-1',
          jobs: [
            { id: 1, table_name: 'FAST_TABLE', phase: 'data', state: 'COMPLETED', rows_expected: 100000, rows_copied: 100000, bytes_copied: 10000000, attempt: 1 },
            { id: 2, table_name: 'MED_TABLE', phase: 'data', state: 'COMPLETED', rows_expected: 100000, rows_copied: 100000, bytes_copied: 10000000, attempt: 1 },
            { id: 3, table_name: 'SLOW_TABLE', phase: 'data', state: 'COMPLETED', rows_expected: 100000, rows_copied: 100000, bytes_copied: 10000000, attempt: 1 },
          ],
          pipeline_stats: [
            // duration_ms=500 → "500ms"
            { table: 'FAST_TABLE', partition: '', rows: 100000, speed: 200000, percent: 100, state: 'STATS', read_time_ms: 150, write_time_ms: 350, read_pct: 30, write_pct: 70, batches: 2, avg_batch_ms: 175, duration_ms: 500, acquire_wait_ms: 5, commit_wait_ms: 20 },
            // duration_ms=15000 → "15.0s"
            { table: 'MED_TABLE', partition: '', rows: 100000, speed: 6667, percent: 100, state: 'STATS', read_time_ms: 4500, write_time_ms: 10500, read_pct: 30, write_pct: 70, batches: 10, avg_batch_ms: 1050, duration_ms: 15000, acquire_wait_ms: 50, commit_wait_ms: 300 },
            // duration_ms=125000 → "2m 5s"
            { table: 'SLOW_TABLE', partition: '', rows: 100000, speed: 800, percent: 100, state: 'STATS', read_time_ms: 37500, write_time_ms: 87500, read_pct: 30, write_pct: 70, batches: 50, avg_batch_ms: 1750, duration_ms: 125000, acquire_wait_ms: 100, commit_wait_ms: 1000 },
          ],
        }),
      });
    });

    await page.route(`**/api/projects/${projectId}/progress`, (route) => {
      route.abort();
    });

    await page.goto(`/projects/${projectId}/migrate`);
    await expect(page.getByText('Pipeline Analytics')).toBeVisible({ timeout: 10000 });

    // Verify millisecond formatting: 500ms
    const fastRow = page.locator('tr').filter({ hasText: 'FAST_TABLE' }).first();
    await expect(fastRow.getByText('500ms')).toBeVisible();

    // Verify seconds formatting: 15.0s
    const medRow = page.locator('tr').filter({ hasText: 'MED_TABLE' }).first();
    await expect(medRow.getByText('15.0s')).toBeVisible();

    // Verify minutes formatting: 2m 5s
    const slowRow = page.locator('tr').filter({ hasText: 'SLOW_TABLE' }).first();
    await expect(slowRow.getByText('2m 5s')).toBeVisible();
  });
});

test.describe('Pipeline Analytics — Speed Formatting', () => {
  test('speed shows as k/s format', async ({ page }) => {
    await setupMigrationPage(page);
    await page.goto('/projects/test-proj/migrate');

    await expect(page.getByText('Pipeline Analytics')).toBeVisible({ timeout: 10000 });

    // CUSTOMERS has speed calculated from rows/duration: 500000*1000/6000 = 83333 → 83.3k/s
    const customersRow = page.locator('tr').filter({ hasText: 'CUSTOMERS' }).first();
    await expect(customersRow.getByText('83.3k/s')).toBeVisible();
  });
});

test.describe('Pipeline Analytics — Read/Write Percentage Bars', () => {
  test('percentage bars show correct values', async ({ page }) => {
    await setupMigrationPage(page);
    await page.goto('/projects/test-proj/migrate');

    await expect(page.getByText('Pipeline Analytics')).toBeVisible({ timeout: 10000 });

    // CUSTOMERS: read_time=1800, write_time=4200, duration=6000
    // readPct = round(1800/6000*100) = 30%, writePct = round(4200/6000*100) = 70%
    const customersRow = page.locator('tr').filter({ hasText: 'CUSTOMERS' }).first();
    // The row should show "30%" for read and "70%" for write as text
    const readCell = customersRow.locator('td').nth(6); // Read column
    const writeCell = customersRow.locator('td').nth(7); // Write column
    await expect(readCell.getByText('30%')).toBeVisible();
    await expect(writeCell.getByText('70%')).toBeVisible();
  });
});

test.describe('Pipeline Analytics — No Stats Section', () => {
  test('pipeline analytics section hidden when no pipeline_stats', async ({ page }) => {
    const projectId = 'no-stats';

    await page.route(`**/api/projects/${projectId}/status`, (route) => {
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          run_id: 'run-1',
          phase: { phase: 'completed', detail: 'Done' },
          summary: { total: 1, completed: 1, pending: 0, running: 0, failed: 0, retrying: 0, skipped: 0, total_rows: 1000 },
        }),
      });
    });

    await page.route(`**/api/projects/${projectId}/jobs*`, (route) => {
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          run_id: 'run-1',
          jobs: [
            { id: 1, table_name: 'SMALL', phase: 'data', state: 'COMPLETED', rows_expected: 1000, rows_copied: 1000, bytes_copied: 10000, attempt: 1 },
          ],
          pipeline_stats: [],
        }),
      });
    });

    await page.route(`**/api/projects/${projectId}/progress`, (route) => {
      route.abort();
    });

    await page.goto(`/projects/${projectId}/migrate`);

    // Wait for jobs to load (the jobs card should appear)
    await expect(page.getByText('SMALL')).toBeVisible({ timeout: 10000 });

    // Pipeline Analytics should NOT appear
    await expect(page.getByText('Pipeline Analytics')).not.toBeVisible();
  });
});

test.describe('Pipeline Analytics — Single Table No Aggregate', () => {
  test('no aggregate summary when only 1 table exists', async ({ page }) => {
    const projectId = 'single-tbl';

    await page.route(`**/api/projects/${projectId}/status`, (route) => {
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          run_id: 'run-1',
          phase: { phase: 'completed', detail: 'Done' },
          summary: { total: 1, completed: 1, pending: 0, running: 0, failed: 0, retrying: 0, skipped: 0, total_rows: 500000 },
        }),
      });
    });

    await page.route(`**/api/projects/${projectId}/jobs*`, (route) => {
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          run_id: 'run-1',
          jobs: [
            { id: 1, table_name: 'ONLY_TABLE', phase: 'data', state: 'COMPLETED', rows_expected: 500000, rows_copied: 500000, bytes_copied: 50000000, attempt: 1 },
          ],
          pipeline_stats: [
            { table: 'ONLY_TABLE', partition: '', rows: 500000, speed: 50000, percent: 100, state: 'STATS', read_time_ms: 3000, write_time_ms: 7000, read_pct: 30, write_pct: 70, batches: 10, avg_batch_ms: 700, duration_ms: 10000, acquire_wait_ms: 50, commit_wait_ms: 500 },
          ],
        }),
      });
    });

    await page.route(`**/api/projects/${projectId}/progress`, (route) => {
      route.abort();
    });

    await page.goto(`/projects/${projectId}/migrate`);
    await expect(page.getByText('Pipeline Analytics')).toBeVisible({ timeout: 10000 });

    // The ONLY_TABLE row should be visible
    await expect(page.locator('tr').filter({ hasText: 'ONLY_TABLE' }).first()).toBeVisible();

    // Aggregate summary should NOT appear (only shows when statsTableMap.size > 1)
    await expect(page.getByText('Aggregate:')).not.toBeVisible();
    await expect(page.getByText('Primary bottleneck:')).not.toBeVisible();
  });
});

test.describe('Pipeline Analytics — Mixed Bottleneck Aggregate', () => {
  test('aggregate shows dominant bottleneck from mixed tables', async ({ page }) => {
    const projectId = 'mixed-bn';

    await page.route(`**/api/projects/${projectId}/status`, (route) => {
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          run_id: 'run-1',
          phase: { phase: 'completed', detail: 'Done' },
          summary: { total: 2, completed: 2, pending: 0, running: 0, failed: 0, retrying: 0, skipped: 0, total_rows: 200000 },
        }),
      });
    });

    await page.route(`**/api/projects/${projectId}/jobs*`, (route) => {
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          run_id: 'run-1',
          jobs: [
            { id: 1, table_name: 'ORA_HEAVY', phase: 'data', state: 'COMPLETED', rows_expected: 100000, rows_copied: 100000, bytes_copied: 10000000, attempt: 1 },
            { id: 2, table_name: 'PG_HEAVY', phase: 'data', state: 'COMPLETED', rows_expected: 100000, rows_copied: 100000, bytes_copied: 10000000, attempt: 1 },
          ],
          pipeline_stats: [
            // Oracle Read bottleneck: read_pct=80, write_pct=20
            { table: 'ORA_HEAVY', partition: '', rows: 100000, speed: 5000, percent: 100, state: 'STATS', read_time_ms: 16000, write_time_ms: 4000, read_pct: 80, write_pct: 20, batches: 2, avg_batch_ms: 2000, duration_ms: 20000, acquire_wait_ms: 10, commit_wait_ms: 50 },
            // PG Write bottleneck: read_pct=20, write_pct=80
            { table: 'PG_HEAVY', partition: '', rows: 100000, speed: 5000, percent: 100, state: 'STATS', read_time_ms: 4000, write_time_ms: 16000, read_pct: 20, write_pct: 80, batches: 10, avg_batch_ms: 1600, duration_ms: 20000, acquire_wait_ms: 10, commit_wait_ms: 50 },
          ],
        }),
      });
    });

    await page.route(`**/api/projects/${projectId}/progress`, (route) => {
      route.abort();
    });

    await page.goto(`/projects/${projectId}/migrate`);
    await expect(page.getByText('Pipeline Analytics')).toBeVisible({ timeout: 10000 });

    // Individual bottlenecks
    const oraRow = page.locator('tr').filter({ hasText: 'ORA_HEAVY' }).first();
    await expect(oraRow.getByText('Oracle Read')).toBeVisible();

    const pgRow = page.locator('tr').filter({ hasText: 'PG_HEAVY' }).first();
    await expect(pgRow.getByText('PG Write')).toBeVisible();

    // Aggregate: totalRead = 16000+4000=20000, totalWrite = 4000+16000=20000, totalDur = 20000+20000=40000
    // avgReadPct = round(20000/40000*100) = 50, avgWritePct = round(20000/40000*100) = 50
    // Since readPct is NOT > writePct (50 == 50), bottleneck = 'PG Write'
    await expect(page.getByText('Primary bottleneck: PG Write')).toBeVisible();
  });
});
