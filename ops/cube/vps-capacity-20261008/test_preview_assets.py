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
 def test_comments_strings_and_regex_are_not_imports(self):
  self.assertEqual(a.imports('/node_modules/.vite/deps/react.js',b'''// import MyComponent from './MyComponent'
/* export {sample} from './example' */
const docs = "import x from './fake.js'";
const pattern = /import x from 'regex'/;
import {real} from './actual.js';
export * from './exports.js';'''),['/node_modules/.vite/deps/actual.js','/node_modules/.vite/deps/exports.js'])
 def test_production_bundles_are_checked_without_executing_them(self):
  self.assertEqual(a.entries(b'<script type="module" src="/assets/index-a1.js"></script><link rel="stylesheet" href="/assets/index-a1.css">'),['/assets/index-a1.js','/assets/index-a1.css'])
  self.assertEqual(a.imports('/assets/index-a1.js',b'import("./chunk-b2.js"); import("/api/model");'),['/assets/chunk-b2.js'])
 def test_static_subdirectory_assets(self):
  self.assertEqual(a.entries(b'<link rel="stylesheet" href="css/styles.css"><script src="js/chatbot.js"></script>','/club-info/index.html','/club-info/'),['/club-info/css/styles.css','/club-info/js/chatbot.js'])
  self.assertEqual(a.imports('/club-info/js/chatbot.js',b'import("./ui.js"); fetch("/api/model");','/club-info/'),['/club-info/js/ui.js'])
  self.assertIsNone(a.local_module('/club-info/index.html','/club-info/api/start','/club-info/'))
 def test_redirect_scope(self):
  self.assertEqual(a.redirect_path('/','/club-info/index.html'),'/club-info/index.html')
  for path in ['https://model.example/index.html','//model.example/index.html','/api/model','/index.html?start=1']:
   with self.assertRaises(AssertionError):a.redirect_path('/',path)
 def test_escape_cannot_turn_into_api_request(self):
  self.assertIsNone(a.local_module('/src/main.tsx','../../api/start-task'))
  self.assertIsNone(a.local_module('/src/main.tsx','react'))
  self.assertEqual(a.local_module('/src/components/A.tsx','../app.css?import'),'/src/app.css?import')
if __name__=='__main__':unittest.main()
