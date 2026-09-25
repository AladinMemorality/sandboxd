import copy
import unittest
from amend_manifest import amend
from plan import Invalid

class AmendTests(unittest.TestCase):
    def test_preserves_disk_and_original_and_binds_exact_template(self):
        original={'format':1,'purpose':'CUBE_CURRENT_DISK_RESCUE_INPUT','sandbox_id':'owned','source_metadata_sha256':{'cubebox':'a','storage':'b'},'upper_subdir':'disk/owned/upper','artifacts':[{'file':'current.ext4','sha256':'current'},{'file':'lower-000.ext4','sha256':'lower'}]}
        before=copy.deepcopy(original)
        plan={'format':1,'purpose':'CUBE_CURRENT_DISK_RESCUE','sandbox_id':'owned','source_metadata_sha256':{'cubebox':'a','storage':'b','template_snapshot_metadata':'c'},'upper_subdir':'disk/tpl-reviewed_0/upper','upper_identity':{'kind':'template_snapshot','template_id':'tpl-reviewed','container_id':'tpl-reviewed_0','metadata_sha256':'c'}}
        new=amend(original,plan,'old-hash','plan-hash')
        self.assertEqual(original,before)
        self.assertEqual(new['artifacts'],original['artifacts'])
        self.assertEqual(new['amendment']['original_manifest_sha256'],'old-hash')
        for k,v in [('sandbox_id','other'),('upper_subdir','disk/other/upper')]:
            wrong=copy.deepcopy(plan);wrong[k]=v
            with self.assertRaises(Invalid):amend(original,wrong,'x','y')
        plan['source_metadata_sha256']['storage']='changed'
        with self.assertRaises(Invalid):amend(original,plan,'x','y')
