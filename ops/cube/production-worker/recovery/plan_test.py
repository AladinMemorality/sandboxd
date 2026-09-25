import copy
import unittest
from plan import Invalid, make_plan

def fixture():
    box = {'ID':'owned','sandbox_id':'owned','namespace':'default','first_container_name':'owned',
           'Containers':{'owned':{'ID':'owned','sandbox_id':'owned','config':{'envs':[{'key':'SECRET','value':'never-export'}], 'volume_mounts':[{'name':'rw','container_path':'/'}]},
            'cube_rootfs_info':{'overlay_info':{'virtiofs_lower_dir':['top/fs','base/fs']}}}},
           'virtiofs_config_map':{'default':{'backendfs_config':{'shared_dir':'/data/cubelet/','read_only':True,'allowed_dirs':['/data/cubelet/layers/base','/data/cubelet/layers/top']}}}}
    storage={'SandboxID':'owned','Namespace':'default','Volumes':{'rw':{'Name':'rw','FilePath':'/data/cubelet/storage/current','VolumeName':'current-generation','Kind':'volume','Gen':7,'Type':'ext4'},'memory':{'FilePath':'/missing-memory'}}}
    return box,storage

class Plans(unittest.TestCase):
    def test_uses_current_volume_without_ram_and_preserves_layer_order(self):
        b,s=fixture(); p=make_plan(b,s,'owned')
        self.assertEqual(p['current_disk']['Gen'],7)
        self.assertEqual(p['lower_dirs'],['/data/cubelet/layers/top/fs','/data/cubelet/layers/base/fs'])
        self.assertEqual(p['upper_subdir'],'disk/owned/upper')
        self.assertNotIn('never-export',str(p))
        self.assertNotIn('/missing-memory',str(p))
    def test_exact_cli_raw_protobuf_schema(self):
        b,s=fixture()
        raw={'namespace':'default','sandboxID':'owned','volumes':[{'name':'rw','file_path':'/data/cubelet/storage/current','volume_name':'current-generation','kind':'volume'}]}
        p=make_plan(b,raw,'owned')
        self.assertEqual(p['current_disk']['Gen'],0) # protobuf default omitted by jsoniter
        self.assertNotIn('Type',p['current_disk']) # CLI does not return filesystem type
        raw['volumes'].append(dict(raw['volumes'][0]))
        with self.assertRaises(Invalid): make_plan(b,raw,'owned')

    def test_snapshot_upper_uses_exact_referenced_metadata_not_new_id(self):
        b,s=fixture();tid='tpl-reviewed'
        b['LocalRunTemplate']={'distributionReference':{'templateID':tid},'snapshot':{'snapshot':{'id':tid,'media':'cubebox','path':'/data/cubelet/storage/xfs/snapshots/'+tid+'/metadata'}}}
        b['Annotations']={'cube.master.appsnapshot.template.id':tid,'cube.master.launch.memory.snapshot.id':tid}
        meta={'app_snapshot_container_id':tid+'_0'}
        with self.assertRaises(Invalid):make_plan(b,s,'owned')
        p=make_plan(b,s,'owned',meta)
        self.assertEqual(p['upper_subdir'],'disk/tpl-reviewed_0/upper')
        self.assertEqual(p['container_id'],'owned')
        self.assertEqual(p['upper_identity']['metadata_sha256'],p['source_metadata_sha256']['template_snapshot_metadata'])
        for bad in ({},{'app_snapshot_container_id':'other_0'},{'app_snapshot_container_id':'../escape'}):
            with self.assertRaises(Invalid):make_plan(b,s,'owned',bad)
        b['LocalRunTemplate']['snapshot']['snapshot']['id']='other'
        with self.assertRaises(Invalid):make_plan(b,s,'owned',meta)

    def test_live_xfs_snapshot_kind_requires_exact_current_generation(self):
        b,s=fixture();v=s['Volumes']['rw']
        name='sb-owned-rootfs-gen7'
        v.update(Kind='snapshot',VolumeName=name,FilePath='/data/cubelet/storage/xfs/objects/volumes/template/'+name)
        p=make_plan(b,s,'owned');self.assertEqual(p['current_disk']['Kind'],'snapshot')
        for field,value in [('VolumeName','tpl-old-rootfs'),('VolumeName','sb-other-rootfs-gen7'),('Gen',6),('FilePath','/data/cubelet/storage/old/'+name)]:
            bad=copy.deepcopy(s);bad['Volumes']['rw'][field]=value
            with self.assertRaises(Invalid):make_plan(b,bad,'owned')

    def test_canonical_container_map_and_exact_pmem_reference(self):
        b,s=fixture()
        b['ContainersMap']={'ContainerMap':b['Containers']};b['Containers']=None
        b.pop('virtiofs_config_map')
        c=b['ContainersMap']['ContainerMap']['owned'];image='rfs-reviewed'
        c['config']['image']={'image':image,'storage_media':'ext4'}
        c['cube_rootfs_info']={'pmem_file':f'/usr/local/services/cubetoolbox/cubebox_os_image/{image}/{image}.ext4'}
        b['ImageReferences']={image:{'ID':image,'Medium':1,'References':None}}
        p=make_plan(b,s,'owned')
        self.assertEqual(p['lower_layout'],'pmem_ext4')
        self.assertEqual(p['lower_dirs'],[])
        self.assertEqual(p['lower_image']['image_id'],image)
        mutations=[lambda x:x['ImageReferences'][image].update(Medium=0),
                   lambda x:x['ImageReferences'][image].update(ID='other'),
                   lambda x:x['ContainersMap']['ContainerMap']['owned']['cube_rootfs_info'].update(pmem_file='/etc/passwd'),
                   lambda x:x.update(Containers={'other':{}}),
                   lambda x:x['ContainersMap']['ContainerMap'].update(other={})]
        for mutate in mutations:
            bad=copy.deepcopy(b);mutate(bad)
            with self.assertRaises(Invalid):make_plan(bad,s,'owned')

    def test_fails_closed_on_ambiguous_identity_or_storage(self):
        def cases(b,s):
            return [lambda: b.update(ID='other'), lambda: s.update(SandboxID='other'),
                    lambda: s['Volumes']['rw'].update(Kind='snapshot'),
                    lambda: s['Volumes']['rw'].update(FilePath='/etc/passwd'),
                    lambda: s['Volumes']['rw'].update(FilePath='/data/cubelet/a/../other'),
                    lambda: s.update(pluginVolumeBackendInfos={'external':{}}),
                    lambda: b['Containers']['owned']['config']['volume_mounts'].append({'name':'x','container_path':'/home'}),
                    lambda: b['Containers']['owned']['cube_rootfs_info']['overlay_info'].update(virtiofs_lower_dir=['top/../../etc']),
                    lambda: b['virtiofs_config_map']['default']['backendfs_config']['allowed_dirs'].append('/data/cubelet/another/top'),
                    lambda: b['virtiofs_config_map']['default']['backendfs_config'].update(read_only=False),
                    lambda: b['Containers'].update(other=copy.deepcopy(b['Containers']['owned']))]
        for index in range(11):
            b,s=fixture(); cases(b,s)[index]()
            with self.subTest(index=index), self.assertRaises(Invalid): make_plan(b,s,'owned')

if __name__=='__main__': unittest.main()
