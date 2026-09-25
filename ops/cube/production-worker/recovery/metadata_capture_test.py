import unittest
from metadata_capture import select_storage
from plan import Invalid
class MetadataTests(unittest.TestCase):
    def test_selects_exact_id_without_other_rows(self):
        text='other\t{"sandboxID":"other","secret":"private"}\nowned\t{"sandboxID":"owned","volumes":[]}\n'
        self.assertEqual(select_storage(text,'owned'),{'sandboxID':'owned','volumes':[]})
        for text in ('owned\t{"sandboxID":"other"}', 'owned\t{"sandboxID":"owned"}\nowned\t{"sandboxID":"owned"}', 'owned-prefix\t{"sandboxID":"owned-prefix"}'):
            with self.subTest(text=text),self.assertRaises(Invalid): select_storage(text,'owned')
if __name__=='__main__':unittest.main()
