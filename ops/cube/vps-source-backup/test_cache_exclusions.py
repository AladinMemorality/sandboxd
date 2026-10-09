"""Check GNU tar cache exclusions against retained local source/configuration."""
import ast,pathlib,subprocess,tarfile,tempfile,unittest
class CacheExclusions(unittest.TestCase):
 def test_generated_pnpm_store_excluded_but_local_source_and_config_retained(self):
  source=pathlib.Path(__file__).with_name('export-source.py').read_text()
  assignments=[n for n in ast.walk(ast.parse(source)) if isinstance(n,ast.Assign) and any(isinstance(t,ast.Name) and t.id=='exclusions' for t in n.targets)]
  self.assertEqual(len(assignments),1);patterns=ast.literal_eval(assignments[0].value)
  excluded=['.local/share/pnpm/store/v3/files/package','.pnpm-store/v3/files/package','.cache/pnpm/metadata','workspace/app/node_modules/react/index.js']
  retained=['.local/share/pnpm/config/rc','.local/state/pnpm/state.json','workspace/app/pnpm-lock.yaml','workspace/app/packages/local/index.ts','.npmrc','workspace/app/.git/HEAD']
  with tempfile.TemporaryDirectory() as folder:
   root=pathlib.Path(folder);home=root/'home';home.mkdir()
   for name in excluded+retained:
    p=home/name;p.parent.mkdir(parents=True,exist_ok=True);p.write_text(name)
   archive=root/'home.tar'
   subprocess.run(['tar','-cf',str(archive),*['--exclude='+pattern for pattern in patterns],'-C',str(home),'.'],check=True)
   with tarfile.open(archive) as tar:
    members=tar.getmembers()
    for member in members:
     parts=pathlib.PurePosixPath(member.name).parts
     self.assertNotIn('node_modules',parts)
     self.assertFalse(any(parts[:len(prefix)]==prefix for prefix in [('.local','share','pnpm','store'),('.pnpm-store',),('.cache','pnpm')]))
    files={str(pathlib.PurePosixPath(m.name)):tar.extractfile(m).read().decode() for m in members if m.isfile()}
   self.assertEqual(files,{name:name for name in retained})
if __name__=='__main__':unittest.main()
