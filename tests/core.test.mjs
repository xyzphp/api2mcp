import { test } from 'node:test';
import assert from 'node:assert/strict';
await import('../assets/core.js');
const C = globalThis.Api2McpCore;

test('客户端配置把独立 MCP Token 放在各自调用 URL 中', () => {
  for (const slug of ['users', 'orders']) {
    const server = { slug, status: 'running', token: `test-${slug}` };
    const config = JSON.parse(C.clientConfig(server, 'http://localhost:18080/'));
    assert.deepEqual(config.mcpServers[slug], {
      type: 'http', url: `http://localhost:18080/mcp/${slug}?token=test-${slug}`,
    });
    assert.equal(Object.keys(config.mcpServers).length, 1);
  }
});

test('未发布服务无法输出看似可用的客户端配置', () => {
  assert.throws(() => C.clientConfig({ slug: 'draft', status: 'draft', token: '' }, 'http://localhost'), /发布/);
});

test('MCP 代理 JSON 支持带 Query 的上游端点，客户端 Header 仍具有最高优先级', () => {
  const server = { type: 'proxy', slug: 'remote', status: 'running', token: 'gateway-token' };
  const target = 'https://mcp.example.com/nested/mcp/?token=upstream-token&tenant=a%2Bb';
  const config = JSON.parse(C.clientConfig(server, 'http://localhost:18080/http_mcp', '{"authorization":"Bearer client-token"}', { base_url: target, Authorization: 'Bearer saved-token', 'X-Tenant': 'demo' }));
  assert.equal(config.mcpServers.remote.url, 'http://localhost:18080/http_mcp/mcp/remote?token=gateway-token');
  assert.deepEqual(config.mcpServers.remote.headers, { base_url: target, 'X-Tenant': 'demo', authorization: 'Bearer client-token' });
  const overridden = JSON.parse(C.clientConfig(server, 'http://localhost', '{"X-API2MCP-Base-URL":"https://other.example/mcp?key=custom"}', { base_url: target }));
  assert.deepEqual(overridden.mcpServers.remote.headers, { 'X-API2MCP-Base-URL': 'https://other.example/mcp?key=custom' });
  assert.throws(() => C.clientConfig({ ...server, type: 'api' }, 'http://localhost', '{}', { base_url: target }), /查询参数/);
});

test('客户端 JSON 把自定义上游 Header 放进普通 headers，MCP Token 保持在 URL', () => {
  const server = { slug: 'custom', status: 'running', token: 'mcp-access-token' };
  const raw = '{"base_url":"https://api.example.com/v1","Authorization":"Bearer client-token","X-Key":"client-key"}';
  const config = JSON.parse(C.clientConfig(server, 'http://localhost:18080', raw));
  assert.equal(config.mcpServers.custom.url, 'http://localhost:18080/mcp/custom?token=mcp-access-token');
  assert.deepEqual(config.mcpServers.custom.headers, {
    base_url: 'https://api.example.com/v1', Authorization: 'Bearer client-token', 'X-Key': 'client-key',
  });
  assert.doesNotMatch(JSON.stringify(config), /X-API2MCP-Credentials/);
});

test('无效客户端 Header 不会生成可复制的配置', () => {
  for (const raw of ['null', '[]', 'invalid', '{"X-Key":42}', '{"body":[]}', '{"Host":"example.test"}', '{"X-Key":"a","x-key":"b"}', '{"X-API2MCP-Credentials":"{}"}', '{"base_url":"ftp://api.example.com"}']) {
    assert.throws(() => C.clientRequestHeaders(raw));
  }
  assert.throws(() => C.clientRequestHeaders(JSON.stringify({ 'X-Key': 'a'.repeat(17000) })), /16 KB/);
  assert.deepEqual(C.clientRequestHeaders(' { } '), {});
  assert.deepEqual(C.clientRequestHeaders('{\n "X-Key": "value"\n}'), { 'X-Key': 'value' });
});

test('Go 发布版本与页面显示版本一致', () => {
  assert.equal(C.versionLabel({ version: 1 }), 'v1.0');
  assert.equal(C.versionLabel({ version: 2 }), 'v1.1');
});

test('批量凭证支持 Header 原文、URL 编码 Query 和 Cookie 原文', () => {
  assert.deepEqual(C.parseCredentialEntries('Authorization: Token a=b\nX-Tenant: demo', 'header').map(row => [row.name, row.value]), [['Authorization', 'Token a=b'], ['X-Tenant', 'demo']]);
  assert.deepEqual(C.parseCredentialEntries('?api_key=a%2Bb%3D&tenant=%E4%B8%AD%E6%96%87', 'query').map(row => [row.name, row.value]), [['api_key', 'a+b='], ['tenant', '中文']]);
  assert.deepEqual(C.parseCredentialEntries('session=a=b; tenant=demo', 'cookie').map(row => [row.name, row.value]), [['session', 'a=b'], ['tenant', 'demo']]);
});

test('批量 Body 保存原始 JSON，包括大整数和嵌套结构', () => {
  const content = '{"auth":{"token":"demo"},"id":1234567890123456789,"enabled":true}';
  assert.deepEqual(C.parseCredentialEntries(content, 'body'), [{ in: 'body', name: '$', value: content, valueType: 'json', enabled: true }]);
  assert.throws(() => C.parseCredentialEntries('[1,2]', 'body'), /对象/);
  assert.throws(() => C.parseCredentialEntries('{"key":{}}', 'header'), /字符串/);
});

test('批量参数拒绝缺失名称或格式错误，不悄悄丢弃输入', () => {
  assert.throws(() => C.parseCredentialEntries('bad-header-without-colon', 'header'), /Name: Value/);
  assert.throws(() => C.parseCredentialEntries('=secret', 'query'), /参数名/);
  assert.throws(() => C.parseCredentialEntries('', 'query'), /粘贴/);
});

test('API 测试发送类型正确的参数，保留空字符串和重复 Query，省略未启用项', () => {
  const rows = [
    { in: 'path', name: 'id', value: 'a/b', enabled: true, schema: { type: 'string' } },
    { in: 'query', name: 'limit', value: '2', enabled: true, schema: { type: 'integer' } },
    { in: 'query', name: 'tag', value: 'first', enabled: true },
    { in: 'query', name: 'tag', value: 'second', enabled: true },
    { in: 'query', name: 'empty', value: '', enabled: true },
    { in: 'query', name: 'disabled', value: 'secret', enabled: false },
    { in: 'header', name: 'X-Enabled', value: 'false', enabled: true, schema: { type: 'boolean' } },
  ];
  assert.deepEqual(JSON.parse(JSON.stringify(C.testArguments(rows))), { path: { id: 'a/b' }, query: { limit: 2, tag: ['first', 'second'], empty: '' }, header: { 'X-Enabled': false } });
  assert.throws(() => C.testArguments([{ in: 'path', name: 'id', value: '', enabled: true }]), /路径参数/);
  assert.throws(() => C.testArguments([{ in: 'header', name: 'X-Key', value: 'a', enabled: true }, { in: 'header', name: 'x-key', value: 'b', enabled: true }]), /重复/);
});

test('API 测试 Body 使用原始 JSON 字符串传递，保留大整数，不发送空白 Body', () => {
  const raw = '{"id":1234567890123456789,"enabled":false}';
  assert.equal(JSON.parse(JSON.stringify(C.testPayload('op-1', [], raw, 'json'))).body, raw);
  assert.equal(Object.hasOwn(C.testPayload('op-1', [], '  ', 'configured'), 'body'), false);
  assert.throws(() => C.testPayload('op-1', [], '{invalid}', 'json'), /Body/);
  assert.throws(() => C.testValue('1234567890123456789', { type: 'integer' }), /较大的整数/);
  assert.throws(() => C.testValue('1.5', { type: 'integer' }), /integer/);
});

test('API 测试表单和参数名不会修改对象原型', () => {
  const args = C.testArguments([{ in: 'query', name: '__proto__', value: 'literal', enabled: true }]);
  assert.equal(JSON.parse(JSON.stringify(args)).query.__proto__, 'literal');
  const fields = C.testBodyFields(JSON.parse('{"type":"object","properties":{"__proto__":{"type":"string"}},"required":["__proto__"]}'));
  assert.equal(Object.getPrototypeOf(fields.properties), null);
  assert.equal(JSON.stringify(C.testExample({ type: 'object', ...fields })), '{"__proto__":"示例值"}');
});
