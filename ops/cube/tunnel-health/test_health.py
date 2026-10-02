import unittest
from unittest.mock import patch
from email.message import Message
from io import BytesIO
import health


class HealthTest(unittest.TestCase):
    def test_reconnect_after_three_public_failures_even_when_local_is_healthy(self):
        state = {}
        for now in [2000, 2030, 2060]:
            state, action = health.decision(state, {'disconnected': True}, True, True, now)
        self.assertEqual(action, 'reconnect')
        for now in range(2090, 2960, 30):
            state, action = health.decision(state, {'disconnected': True}, True, True, now)
            self.assertEqual(action, 'observe')
        self.assertEqual(health.decision(state, {'disconnected': True}, True, True, 2960)[1], 'reconnect')

    def test_other_errors_stopped_service_and_local_outages_do_not_restart(self):
        for disconnected, local, active in [(False, True, True), (True, False, True), (True, True, False)]:
            state, action = health.decision({'failures': 2, 'checked_at': 2000}, {'disconnected': disconnected}, local, active, 2030)
            self.assertEqual(action, 'observe')
            self.assertEqual(state['failures'], 0)

    def test_gap_and_clock_reversal_cannot_complete_a_stale_failure_streak(self):
        for now in [1000, 2500]:
            state, action = health.decision({'failures': 2, 'checked_at': 2000}, {'disconnected': True}, True, True, now)
            self.assertEqual(action, 'observe')
            self.assertEqual(state['failures'], 1)

    def test_only_cloudflare_530_1033_counts_as_a_disconnected_tunnel(self):
        for status, body, server, ray, expected in [
            (530, b'error code: 1033', 'cloudflare', 'ray', True),
            (530, b'<h1>Error <span>1033</span></h1>', 'cloudflare', 'ray', True),
            (530, b'error code: 1016', 'cloudflare', 'ray', False),
            (502, b'1033', 'cloudflare', 'ray', False),
            (530, b'1033', 'origin', 'ray', False),
            (530, b'1033', 'cloudflare', '', False),
            (404, b'404 page not found', 'cloudflare', 'ray', False),
        ]:
            headers = Message(); headers['Server'] = server; headers['CF-Ray'] = ray
            error = health.urllib.error.HTTPError('https://fixture.invalid', status, 'fixture', headers, BytesIO(body))
            with patch.object(health.urllib.request.OpenerDirector, 'open', side_effect=error):
                self.assertEqual(health.probe('https://fixture.invalid')['disconnected'], expected)

if __name__ == '__main__':
    unittest.main()
