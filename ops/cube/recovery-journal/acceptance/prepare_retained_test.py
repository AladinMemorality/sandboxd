import copy
import unittest
from prepare_retained import validate_fence, validate_inventory

class RetainedPreparationTests(unittest.TestCase):
    def test_complete_exact_source_only_inventory(self):
        owned='a'*32
        good='NODES_SCANNED 1/1\nSANDBOX_COUNT 1\n'+owned+' unknown\n'
        validate_inventory(good, owned)
        for bad in [good.replace('1/1','0/1'),good.replace('COUNT 1','COUNT 0'),
                    good.replace(owned,'b'*32),good+owned+' unknown\n',
                    good+'b'*32+' unknown\n','']:
            with self.assertRaises(ValueError): validate_inventory(bad,owned)

    def test_operator_fence_binds_boot_source_freshness_and_no_execution(self):
        old={'worker_machine_id':'a'*32,'worker_boot_id':'old'}
        good={'purpose':'OWNED_RECOVERY_EXECUTION_FENCE','old_provider_id':'b'*32,
              'worker_machine_id':'a'*32,'previous_boot_id':'old','current_boot_id':'new',
              'no_task_verified':True,'no_owned_vmm_or_disk_fd_verified':True,
              'provider_requests_drained':True,'management_fenced':True,
              'checked_at':995,'expires_at':1100}
        validate_fence(good,old,'b'*32,'new',1000)
        for key,value in [('purpose','CUBE_CURRENT_DISK_CAPTURE'),('old_provider_id','c'*32),
                          ('current_boot_id','old'),('worker_machine_id','c'*32),
                          ('previous_boot_id','wrong'),('checked_at',699),('checked_at',1001),
                          ('expires_at',1000),('expires_at',2201),('no_task_verified',False),
                          ('no_owned_vmm_or_disk_fd_verified',False),('provider_requests_drained',False),
                          ('management_fenced',False)]:
            bad=copy.deepcopy(good);bad[key]=value
            with self.assertRaises(ValueError): validate_fence(bad,old,'b'*32,'new',1000)

if __name__=='__main__':unittest.main()
