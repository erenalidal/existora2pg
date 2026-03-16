import { test, expect } from '@playwright/test';

test.describe('ProjectCreate — UNLOGGED Toggle', () => {
  test.beforeEach(async ({ page }) => {
    // Mock API calls so we don't need a running backend
    await page.route('**/api/projects/**', (route) => {
      route.fulfill({ status: 200, body: JSON.stringify({}) });
    });
    await page.goto('/projects/new');
  });

  test('default batch/fetch values without UNLOGGED', async ({ page }) => {
    const batchInput = page.locator('input[type="number"]').nth(1); // batch_size
    const fetchInput = page.locator('input[type="number"]').nth(2); // fetch_size

    await expect(batchInput).toHaveValue('50000');
    await expect(fetchInput).toHaveValue('5000');
  });

  test('UNLOGGED toggle multiplies batch ×4 and fetch ×2', async ({ page }) => {
    const batchInput = page.locator('input[type="number"]').nth(1);
    const fetchInput = page.locator('input[type="number"]').nth(2);
    const unloggedCheckbox = page.getByLabel('UNLOGGED tables');

    // Enable UNLOGGED
    await unloggedCheckbox.check();

    await expect(batchInput).toHaveValue('200000');  // 50000 × 4
    await expect(fetchInput).toHaveValue('10000');    // 5000 × 2
  });

  test('UNLOGGED toggle shows hint text', async ({ page }) => {
    const unloggedCheckbox = page.getByLabel('UNLOGGED tables');
    const hint = page.getByText('Batch ×4, Fetch ×2 applied');

    await expect(hint).not.toBeVisible();
    await unloggedCheckbox.check();
    await expect(hint).toBeVisible();
  });

  test('disabling UNLOGGED reverts batch/fetch to original', async ({ page }) => {
    const batchInput = page.locator('input[type="number"]').nth(1);
    const fetchInput = page.locator('input[type="number"]').nth(2);
    const unloggedCheckbox = page.getByLabel('UNLOGGED tables');

    // Enable then disable
    await unloggedCheckbox.check();
    await expect(batchInput).toHaveValue('200000');
    await unloggedCheckbox.uncheck();

    await expect(batchInput).toHaveValue('50000');  // 200000 / 4
    await expect(fetchInput).toHaveValue('5000');   // 10000 / 2
  });

  test('UNLOGGED with custom batch/fetch values', async ({ page }) => {
    const batchInput = page.locator('input[type="number"]').nth(1);
    const fetchInput = page.locator('input[type="number"]').nth(2);
    const unloggedCheckbox = page.getByLabel('UNLOGGED tables');

    // Set custom values first
    await batchInput.fill('100000');
    await fetchInput.fill('8000');

    // Enable UNLOGGED — should multiply current values
    await unloggedCheckbox.check();
    await expect(batchInput).toHaveValue('400000');  // 100000 × 4
    await expect(fetchInput).toHaveValue('16000');   // 8000 × 2
  });

  test('UNLOGGED state persists in form data', async ({ page }) => {
    const unloggedCheckbox = page.getByLabel('UNLOGGED tables');
    await unloggedCheckbox.check();
    await expect(unloggedCheckbox).toBeChecked();

    // Verify hint is visible (confirms state is tracked)
    await expect(page.getByText('Batch ×4, Fetch ×2 applied')).toBeVisible();
  });
});

test.describe('ProjectCreate — Default Form Values', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('**/api/projects/**', (route) => {
      route.fulfill({ status: 200, body: JSON.stringify({}) });
    });
    await page.goto('/projects/new');
  });

  test('validates after migration is checked by default', async ({ page }) => {
    const validateCheckbox = page.getByLabel('Validate after migration');
    await expect(validateCheckbox).toBeChecked();
  });

  test('UNLOGGED is unchecked by default', async ({ page }) => {
    const unloggedCheckbox = page.getByLabel('UNLOGGED tables');
    await expect(unloggedCheckbox).not.toBeChecked();
  });

  test('chunk strategy defaults to auto', async ({ page }) => {
    const chunkSelect = page.locator('select').filter({ hasText: 'ORA_HASH' });
    await expect(chunkSelect).toHaveValue('auto');
  });

  test('naming convention defaults to lowercase', async ({ page }) => {
    const namingSelect = page.locator('select').filter({ hasText: 'lowercase' });
    await expect(namingSelect).toHaveValue('lowercase');
  });
});

test.describe('ProjectCreate — Edit Mode with UNLOGGED', () => {
  test('loads UNLOGGED state from existing project', async ({ page }) => {
    // Mock project export endpoint with unlogged=true
    await page.route('**/api/projects/test-123/export', (route) => {
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          name: 'Test Project',
          oracle_dsn: 'oracle://user:pass@localhost:1521/ORCL',
          oracle_schema: 'TEST',
          pg_dsn: 'postgres://user:pass@localhost:5432/testdb',
          pg_schema: 'public',
          migration_config: {
            workers: 4,
            batch_size: 200000,
            fetch_size: 10000,
            chunk_strategy: 'ora_hash',
            validate_after: true,
            drop_target: false,
            unlogged: true,
            naming_convention: 'lowercase',
            include_tables: [],
            exclude_tables: [],
          },
        }),
      });
    });
    await page.route('**/api/projects/test-123/test-oracle', (route) => {
      route.fulfill({ status: 200, body: JSON.stringify({ success: false, message: 'no connection' }) });
    });
    await page.route('**/api/projects/test-123/test-postgres', (route) => {
      route.fulfill({ status: 200, body: JSON.stringify({ success: false, message: 'no connection' }) });
    });

    await page.goto('/projects/test-123/edit');

    // Wait for project to load — Project Name input is the first .form-input
    await expect(page.locator('input.form-input').first()).toHaveValue('Test Project', { timeout: 5000 });

    const unloggedCheckbox = page.getByLabel('UNLOGGED tables');
    await expect(unloggedCheckbox).toBeChecked();

    // Batch/fetch should reflect saved values (not multiplied again)
    const batchInput = page.locator('input[type="number"]').nth(1);
    const fetchInput = page.locator('input[type="number"]').nth(2);
    await expect(batchInput).toHaveValue('200000');
    await expect(fetchInput).toHaveValue('10000');
  });
});

test.describe('ProjectCreate — UNLOGGED Toggle Edge Cases', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('**/api/projects/**', (route) => {
      route.fulfill({ status: 200, body: JSON.stringify({}) });
    });
    await page.goto('/projects/new');
  });

  test('multiple toggle cycles ON→OFF→ON preserve correct values', async ({ page }) => {
    const batchInput = page.locator('input[type="number"]').nth(1);
    const fetchInput = page.locator('input[type="number"]').nth(2);
    const unloggedCheckbox = page.getByLabel('UNLOGGED tables');

    // Defaults: batch=50000, fetch=5000
    await expect(batchInput).toHaveValue('50000');
    await expect(fetchInput).toHaveValue('5000');

    // Toggle ON → 200000, 10000
    await unloggedCheckbox.check();
    await expect(batchInput).toHaveValue('200000');
    await expect(fetchInput).toHaveValue('10000');

    // Toggle OFF → back to 50000, 5000
    await unloggedCheckbox.uncheck();
    await expect(batchInput).toHaveValue('50000');
    await expect(fetchInput).toHaveValue('5000');

    // Toggle ON again → 200000, 10000
    await unloggedCheckbox.check();
    await expect(batchInput).toHaveValue('200000');
    await expect(fetchInput).toHaveValue('10000');
  });

  test('UNLOGGED toggle with extremely small batch values', async ({ page }) => {
    const batchInput = page.locator('input[type="number"]').nth(1);
    const fetchInput = page.locator('input[type="number"]').nth(2);
    const unloggedCheckbox = page.getByLabel('UNLOGGED tables');

    // Set small values
    await batchInput.fill('100');
    await fetchInput.fill('100');

    // Toggle ON: 100×4=400, 100×2=200
    await unloggedCheckbox.check();
    await expect(batchInput).toHaveValue('400');
    await expect(fetchInput).toHaveValue('200');
  });

  test('UNLOGGED toggle with maximum edge values', async ({ page }) => {
    const batchInput = page.locator('input[type="number"]').nth(1);
    const fetchInput = page.locator('input[type="number"]').nth(2);
    const unloggedCheckbox = page.getByLabel('UNLOGGED tables');

    // Set large values
    await batchInput.fill('1000000');
    await fetchInput.fill('50000');

    // Toggle ON: 1000000×4=4000000, 50000×2=100000
    await unloggedCheckbox.check();
    await expect(batchInput).toHaveValue('4000000');
    await expect(fetchInput).toHaveValue('100000');

    // Toggle OFF: revert
    await unloggedCheckbox.uncheck();
    await expect(batchInput).toHaveValue('1000000');
    await expect(fetchInput).toHaveValue('50000');
  });
});

test.describe('ProjectCreate — Workers Validation', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('**/api/projects/**', (route) => {
      route.fulfill({ status: 200, body: JSON.stringify({}) });
    });
    await page.goto('/projects/new');
  });

  test('workers field accepts valid numbers', async ({ page }) => {
    const workersInput = page.locator('input[type="number"]').nth(0);

    await workersInput.fill('1');
    await expect(workersInput).toHaveValue('1');

    await workersInput.fill('16');
    await expect(workersInput).toHaveValue('16');

    await workersInput.fill('64');
    await expect(workersInput).toHaveValue('64');
  });

  test('workers field has correct min/max attributes', async ({ page }) => {
    const workersInput = page.locator('input[type="number"]').nth(0);
    await expect(workersInput).toHaveAttribute('min', '1');
    await expect(workersInput).toHaveAttribute('max', '64');
  });
});

test.describe('ProjectCreate — Save Payload', () => {
  test('save button sends correct payload with unlogged field', async ({ page }) => {
    let capturedBody: any = null;

    await page.route('**/api/projects', (route) => {
      if (route.request().method() === 'POST') {
        capturedBody = JSON.parse(route.request().postData() || '{}');
        route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ id: 'new-proj-1', name: capturedBody.name }),
        });
      } else {
        route.fulfill({ status: 200, body: JSON.stringify({}) });
      }
    });

    // Also mock the redirect target
    await page.route('**/api/projects/new-proj-1/**', (route) => {
      route.fulfill({ status: 200, body: JSON.stringify({}) });
    });

    await page.goto('/projects/new');

    // Fill required fields
    const nameInput = page.locator('input.form-input').first();
    await nameInput.fill('Payload Test');

    // Fill Oracle connection fields so DSN can be built
    const oracleHost = page.locator('input.form-input.text-mono').nth(0);
    const oraclePort = page.locator('input.form-input.text-mono').nth(1);
    const oracleUser = page.locator('input.form-input.text-mono').nth(2);
    const oraclePass = page.locator('input.form-input.text-mono').nth(3);
    const oracleService = page.locator('input.form-input.text-mono').nth(4);
    await oracleHost.fill('orahost');
    await oraclePort.fill('1521');
    await oracleUser.fill('orauser');
    await oraclePass.fill('orapass');
    await oracleService.fill('ORCL');

    // Fill PG connection fields
    const pgHost = page.locator('input.form-input.text-mono').nth(5);
    const pgPort = page.locator('input.form-input.text-mono').nth(6);
    const pgUser = page.locator('input.form-input.text-mono').nth(7);
    const pgPass = page.locator('input.form-input.text-mono').nth(8);
    const pgDb = page.locator('input.form-input.text-mono').nth(9);
    await pgHost.fill('pghost');
    await pgPort.fill('5432');
    await pgUser.fill('pguser');
    await pgPass.fill('pgpass');
    await pgDb.fill('pgdb');

    // Enable UNLOGGED
    const unloggedCheckbox = page.getByLabel('UNLOGGED tables');
    await unloggedCheckbox.check();

    // Click save
    await page.getByText('Create Project').click();

    // Wait for navigation (save was triggered)
    await page.waitForURL('**/projects/new-proj-1', { timeout: 5000 });

    // Verify payload
    expect(capturedBody).toBeTruthy();
    expect(capturedBody.name).toBe('Payload Test');
    expect(capturedBody.migration_config.unlogged).toBe(true);
    expect(capturedBody.migration_config.batch_size).toBe(200000); // 50000 × 4
    expect(capturedBody.migration_config.fetch_size).toBe(10000);  // 5000 × 2
    expect(capturedBody.migration_config.validate_after).toBe(true);
  });
});
