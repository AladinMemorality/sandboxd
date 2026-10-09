// Parse source as data. Never evaluate customer JavaScript.
const fs = require('node:fs');
const acorn = require('acorn');
const source = fs.readFileSync(0, 'utf8');
if (Buffer.byteLength(source) > 2 * 1024 * 1024) throw new Error('Module limit');
const ast = acorn.parse(source, {ecmaVersion: 'latest', sourceType: 'module', allowHashBang: true});
const stack = [ast];
const imports = [];
while (stack.length) {
  const node = stack.pop();
  if (!node || typeof node !== 'object') continue;
  if (['ImportDeclaration', 'ExportNamedDeclaration', 'ExportAllDeclaration', 'ImportExpression'].includes(node.type)
      && node.source?.type === 'Literal' && typeof node.source.value === 'string') imports.push(node.source.value);
  for (const value of Object.values(node)) {
    if (Array.isArray(value)) stack.push(...value);
    else if (value && typeof value === 'object') stack.push(value);
  }
}
process.stdout.write(JSON.stringify(imports.reverse()));
