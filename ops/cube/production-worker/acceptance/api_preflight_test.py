import unittest
from api_preflight import validate_handoff, empty_cli_inventory

class PreflightTests(unittest.TestCase):
    def test_exact_short_lived_handoff(self):
        base = dict(purpose='DISPOSABLE_CUBE_API_HANDOFF', family='postgres', run_id='one',
                    no_customer_guests=True, previous_family_cleanup_verified=True, expires_at=1100)
        self.assertTrue(validate_handoff(base, 'postgres', 'one', 1000))
        self.assertFalse(validate_handoff(base, 'reload', 'one', 1000))
        self.assertFalse(validate_handoff(base, 'postgres', 'two', 1000))
        for changes in [dict(expires_at=999), dict(expires_at=4000), dict(no_customer_guests=False),
                        dict(previous_family_cleanup_verified=False)]:
            self.assertFalse(validate_handoff(base | changes, 'postgres', 'one', 1000))

    def test_inventory_must_be_exact_zero(self):
        self.assertTrue(empty_cli_inventory('heading\nSANDBOX_COUNT    0\n'))
        for text in ['', 'SANDBOX_COUNT    1', 'SANDBOX_COUNT    01', 'SANDBOX_COUNT    10',
                     'SANDBOX_COUNT    0\nSANDBOX_COUNT    2', 'prefix SANDBOX_COUNT    0']:
            self.assertFalse(empty_cli_inventory(text))

if __name__ == '__main__': unittest.main()
