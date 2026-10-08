import importlib.util,pathlib,unittest
spec=importlib.util.spec_from_file_location('assets',pathlib.Path(__file__).with_name('preview-assets.py'));a=importlib.util.module_from_spec(spec);spec.loader.exec_module(a)
class Assets(unittest.TestCase):
 def test_local_graph_avoids_external_services(self):
  self.assertEqual(a.entries(b'<script type="module" src="/src/main.tsx"></script><script src="https://model.example/invoke"></script>'),['/src/main.tsx'])
  self.assertEqual(a.imports('/src/main.tsx',b'''import React from "/node_modules/.vite/deps/react.js?v=123";
import "./app.css";
export {App} from "./App.tsx";
const lazy = import('./Lazy.tsx');
fetch('/api/model'); import('/api/model'); import('https://model.example/call'); import('//model.example/call');'''),['/node_modules/.vite/deps/react.js?v=123','/src/app.css','/src/App.tsx','/src/Lazy.tsx'])
 def test_escape_cannot_turn_into_api_request(self):
  self.assertIsNone(a.local_module('/src/main.tsx','../../api/start-task'))
  self.assertIsNone(a.local_module('/src/main.tsx','react'))
  self.assertEqual(a.local_module('/src/components/A.tsx','../app.css?import'),'/src/app.css?import')
if __name__=='__main__':unittest.main()
