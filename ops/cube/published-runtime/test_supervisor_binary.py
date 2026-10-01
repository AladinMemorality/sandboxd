import struct
import unittest
from guest import require_static_supervisor


def executable(*segments):
    data = bytearray(64 + 56 * len(segments))
    data[:8] = b'\x7fELF\x02\x01\x01\x00'
    struct.pack_into('<HHI', data, 16, 2, 62, 1)
    struct.pack_into('<Q', data, 32, 64)
    struct.pack_into('<HH', data, 54, 56, len(segments))
    for i, kind in enumerate(segments):
        struct.pack_into('<I', data, 64 + 56 * i, kind)
    return data


class SupervisorBinaryTests(unittest.TestCase):
    def test_static_executable(self):
        require_static_supervisor(executable(6, 1, 1, 4))

    def test_loader_dependent_executable_is_rejected(self):
        with self.assertRaisesRegex(ValueError, 'static Linux/amd64'):
            require_static_supervisor(executable(6, 3, 1, 2))

    def test_invalid_or_incompatible_executables(self):
        wrong_arch = executable(1)
        struct.pack_into('<H', wrong_arch, 18, 183)
        shared_library = executable(1)
        struct.pack_into('<H', shared_library, 16, 3)
        for data in [b'', b'#!/bin/sh', executable(1)[:-1], executable(4), executable(), wrong_arch, shared_library]:
            with self.subTest(data=bytes(data[:20])):
                with self.assertRaises(ValueError):
                    require_static_supervisor(data)

if __name__ == '__main__':
    unittest.main()
