const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

const htmlPath = path.join(__dirname, '..', 'index.html');
const readmePath = path.join(__dirname, '..', 'README.md');
const workflowPath = path.join(__dirname, '..', '..', '..', '.github', 'workflows', 'wikistr-pages.yml');
const cnamePath = path.join(__dirname, '..', 'CNAME');

const redirectTarget = 'https://nos.trustroots.org/examples/wikistr/';

test('index.html redirects to nostroots wikistr', () => {
  const html = fs.readFileSync(htmlPath, 'utf8');
  assert.match(html, new RegExp(redirectTarget.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')));
  assert.match(html, /location\.replace\(.+ \+ location\.hash\)/);
});

test('README points at nostroots wikistr', () => {
  const readme = fs.readFileSync(readmePath, 'utf8');
  assert.match(readme, /nos\.trustroots\.org\/examples\/wikistr/);
});

test('Pages workflow uploads apps/wikistr static files only', () => {
  const workflow = fs.readFileSync(workflowPath, 'utf8');
  assert.match(workflow, /path: apps\/wikistr/);
  assert.doesNotMatch(workflow, /build-info\.json/);
});

test('CNAME still serves wikistr.trustroots.org', () => {
  assert.equal(fs.readFileSync(cnamePath, 'utf8').trim(), 'wikistr.trustroots.org');
});
