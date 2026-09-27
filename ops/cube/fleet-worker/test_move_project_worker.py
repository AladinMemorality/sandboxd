import hashlib,importlib.util,tempfile,unittest,zipfile
from pathlib import Path
s=importlib.util.spec_from_file_location('worker',Path(__file__).with_name('move-project-worker.py'));w=importlib.util.module_from_spec(s);s.loader.exec_module(w)

class StateComparison(unittest.TestCase):
    def archive(self,path,entries):
        with zipfile.ZipFile(path,'w') as z:
            for name,data,mode in entries:
                item=zipfile.ZipInfo(name);item.external_attr=mode;z.writestr(item,data)
    def test_template_extra_does_not_hide_customer_changes(self):
        with tempfile.TemporaryDirectory() as folder:
            source=Path(folder)/'source.zip';target=Path(folder)/'target.zip'
            customer=('workspace.txt',b'customer state',0o100640<<16)
            stock=('.bash_logout',b'reviewed template',0o100644<<16)
            reviewed={stock[0]:[stock[2],len(stock[1]),hashlib.sha256(stock[1]).hexdigest()]}
            self.archive(source,[customer]);self.archive(target,[stock,customer])
            self.assertEqual(w.digest(source),w.digest(target,reviewed))
            for changed in [('workspace.txt',b'changed state',customer[2]),('workspace.txt',customer[1],0o100600<<16)]:
                self.archive(target,[stock,changed]);self.assertNotEqual(w.digest(source),w.digest(target,reviewed))
            self.archive(target,[stock,customer,('unclassified',b'extra',customer[2])])
            self.assertNotEqual(w.digest(source),w.digest(target,reviewed))
    def test_reviewed_template_file_must_exist_and_match(self):
        with tempfile.TemporaryDirectory() as folder:
            target=Path(folder)/'target.zip';mode=0o100644<<16
            reviewed={'.bash_logout':[mode,4,hashlib.sha256(b'safe').hexdigest()]}
            for entries in [[],[('.bash_logout',b'evil',mode)],[('.bash_logout',b'safe',0o100777<<16)]]:
                self.archive(target,entries)
                with self.assertRaises(AssertionError):w.digest(target,reviewed)
    def test_empty_template_cache_does_not_hide_cache_content(self):
        with tempfile.TemporaryDirectory() as folder:
            source=Path(folder)/'source.zip';target=Path(folder)/'target.zip'
            item=('app.txt',b'customer',0o100644<<16);mode=0o40755<<16|16
            reviewed={'.cache/':[mode,0,hashlib.sha256(b'').hexdigest()]}
            self.archive(source,[item]);self.archive(target,[item,('.cache/',b'',mode)])
            self.assertEqual(w.digest(source),w.digest(target,reviewed))
            self.archive(target,[item,('.cache/',b'',mode),('.cache/new-file',b'unexpected',0o100644<<16)])
            self.assertNotEqual(w.digest(source),w.digest(target,reviewed))
if __name__=='__main__':unittest.main()
