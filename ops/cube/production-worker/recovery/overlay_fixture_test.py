import io
import tarfile
import unittest
from archive_validate_test import archive, file
from archive_validate import validate
from overlay_fixture import expected_semantics
from plan import Invalid

class OverlaySemanticAssertions(unittest.TestCase):
    def entries(self, reverse=False):
        regular, linked = ('hard.txt', 'target.txt') if reverse else ('target.txt', 'hard.txt')
        return [file('workspace/app/overwrite.txt', b'latest'),
                file('workspace/app/removed-dir/new.txt', b'visible recreated opaque directory'),
                file('.cache/opaque/higher.txt', b'higher opaque layer retained'),
                file('workspace/app/'+regular, b'hardlinked data', mode=0o640),
                ('workspace/app/'+linked, b'', tarfile.LNKTYPE, 'workspace/app/'+regular, {'mode':0o640}),
                ('workspace/app/symbolic', b'', tarfile.SYMTYPE, 'target.txt')]

    def check(self, entries):
        stream=io.BytesIO(archive(entries))
        _,index=validate(stream,return_index=True)
        expected_semantics(index,stream)

    def test_either_hardlink_archive_order(self):
        self.check(self.entries())
        self.check(self.entries(True))

    def test_overlay_regressions_rejected(self):
        for absent in ['workspace/app/deleted.txt', 'workspace/app/removed-dir/hidden.txt', '.cache/opaque/lower-hidden.txt']:
            with self.assertRaises(Invalid):self.check(self.entries()+[file(absent)])
        entries=self.entries();entries[0]=file('workspace/app/overwrite.txt',b'older')
        with self.assertRaises(Invalid):self.check(entries)
