(function (root) {
  'use strict';
  // These are display helpers only. Parsing, publishing, tokens and persistence
  // belong to the authenticated Go backend.
  const uid = () => root.crypto?.randomUUID?.() || Math.random().toString(36).slice(2, 12);
  const versionLabel = server => `v1.${Math.max(0, server.version - 1)}`;
  const endpointFor = (server, origin) => `${origin.replace(/\/+$/, '')}/mcp/${server.slug}`;
  const mcpURLFor = (server, origin) => {
    const url = new URL(endpointFor(server, origin));
    url.searchParams.set('token', server.token);
    return url.toString();
  };
  function clientRequestHeaders(text = '{}') {
    let value;
    try { value = JSON.parse(text); } catch { throw new Error('客户端 Header JSON 格式不正确。'); }
    const object = value => value && typeof value === 'object' && !Array.isArray(value);
    if (!object(value)) throw new Error('客户端 Header 配置需要使用 JSON 对象。');
    const blocked = ['host', 'connection', 'content-length', 'content-type', 'transfer-encoding', 'trailer', 'te', 'upgrade', 'proxy-authorization', 'proxy-authenticate', 'x-api2mcp-credentials'];
    const names = new Set();
    let size = 0;
    let baseUrl;
    for (const [name, item] of Object.entries(value)) {
      if (typeof item !== 'string') throw new Error('Header 名称和值需要使用字符串。');
      const key = name.toLowerCase();
      if (!name || name.length > 256 || !/^[!#$%&'*+.^_`|~a-zA-Z0-9-]+$/.test(name) || blocked.includes(key) || /[\x00-\x08\x0a-\x1f\x7f]/.test(item)) throw new Error('Header 名称或值无效，不能覆盖 HTTP 传输控制字段。');
      if (names.has(key)) throw new Error('Header 名称不区分大小写，不能重复。');
      names.add(key);
      size += new TextEncoder().encode(name).length + new TextEncoder().encode(item).length;
      if (size > 16384 || names.size > 100) throw new Error('客户端 Header 总长度最多 16 KB，且最多 100 项。');
      if (key === 'base_url' || key === 'x-api2mcp-base-url') {
        if (baseUrl) throw new Error('base_url 和 X-API2MCP-Base-URL 只能配置一个。');
        baseUrl = item;
      }
    }
    if (baseUrl) {
      let parsed;
      try { parsed = new URL(baseUrl); } catch { throw new Error('base_url 需要是有效的 HTTP 或 HTTPS 地址。'); }
      if (!['http:', 'https:'].includes(parsed.protocol) || !parsed.hostname || parsed.username || parsed.password || parsed.search || parsed.hash) throw new Error('base_url 需要是有效的 HTTP 或 HTTPS API 基础地址，不能包含凭证、查询参数或片段。');
    }
    return value;
  }
  function mergeClientHeaders(defaultHeaders, overrideHeaders) {
    const merged = new Map();
    for (const [name, value] of Object.entries(clientRequestHeaders(JSON.stringify(defaultHeaders || {})))) {
      merged.set(name.toLowerCase(), [name, value]);
    }
    for (const [name, value] of Object.entries(overrideHeaders)) {
      const key = name.toLowerCase();
      const previous = merged.get(key);
      if (previous) merged.delete(key);
      merged.set(key, [name, value]);
    }
    return clientRequestHeaders(JSON.stringify(Object.fromEntries(merged.values())));
  }
  function clientConfig(server, origin, headersText = '{}', defaultHeaders = {}) {
    if (server.status === 'draft' || !server.token) throw new Error('请先发布服务。');
    const headers = mergeClientHeaders(defaultHeaders, clientRequestHeaders(headersText));
    const config = { type: 'http', url: mcpURLFor(server, origin) };
    if (Object.keys(headers).length) config.headers = headers;
    return JSON.stringify({
      mcpServers: {
        [server.slug]: config,
      },
    }, null, 2);
  }
  function parseCredentialEntries(text, location) {
    const content = text.trim();
    if (!content) throw new Error('请先粘贴需要添加的参数。');
    if (!['header', 'query', 'body', 'cookie'].includes(location)) throw new Error('参数位置无效。');
    const entry = (name, value) => ({ in: location, name: name.trim(), value: String(value), valueType: 'string', enabled: true });
    let rows;
    if (content.startsWith('{') || location === 'body') {
      let value;
      try { value = JSON.parse(content); } catch { throw new Error('JSON 格式不正确，请粘贴一个 JSON 对象。'); }
      if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('批量 JSON 需要是一个对象。');
      if (location === 'body') return [{ ...entry('$', content), valueType: 'json' }];
      rows = Object.entries(value).map(([name, item]) => {
        if (item !== null && typeof item === 'object') throw new Error('Header、Query 和 Cookie 的值请使用字符串、数字或布尔值。');
        if (typeof item === 'number' && Number.isInteger(item) && !Number.isSafeInteger(item)) throw new Error('较大的整数请加双引号，避免精度丢失。');
        return entry(name, item ?? '');
      });
    } else if (location === 'query') {
      rows = Array.from(new URLSearchParams(content.replace(/^\?/, '').replace(/\r?\n/g, '&')), ([name, value]) => entry(name, value));
    } else {
      rows = content.split(location === 'cookie' ? /[;\r\n]+/ : /\r?\n/).filter(line => line.trim()).map(line => {
        const separator = line.indexOf(location === 'cookie' ? '=' : ':');
        if (separator < 1) throw new Error(location === 'cookie' ? 'Cookie 格式应为 name=value; name2=value2。' : 'Header 每行格式应为 Name: Value。');
        return entry(line.slice(0, separator), line.slice(separator + 1).trim());
      });
    }
    if (!rows.length || rows.some(row => !row.name)) throw new Error('参数名不能为空。');
    return rows;
  }
  function testValue(text, schema = {}) {
    const type = Array.isArray(schema.type) ? schema.type.find(type => type !== 'null') : schema.type;
    if (!type || type === 'string') return text;
    let value;
    try { value = JSON.parse(text); } catch { throw new Error(`${type} 类型请填写有效 JSON，例如数字、true / false、数组或对象。`); }
    const safe = item => typeof item === 'number' ? Number.isFinite(item) && (!Number.isInteger(item) || Number.isSafeInteger(item)) : Array.isArray(item) ? item.every(safe) : item && typeof item === 'object' ? Object.values(item).every(safe) : true;
    if (!safe(value)) throw new Error('较大的整数请使用 JSON Body 编辑器，或按接口要求填写字符串。');
    const valid = type === 'integer' ? Number.isInteger(value) : type === 'number' ? typeof value === 'number' : type === 'boolean' ? typeof value === 'boolean' : type === 'array' ? Array.isArray(value) : type === 'object' ? value !== null && typeof value === 'object' && !Array.isArray(value) : type === 'null' ? value === null : true;
    if (!valid && !(value === null && Array.isArray(schema.type) && schema.type.includes('null'))) throw new Error(`参数需要 ${type} 类型。`);
    return value;
  }
  function testArguments(rows) {
    const args = Object.create(null), seen = new Map();
    for (const row of rows.filter(row => row.enabled)) {
      const name = row.name.trim();
      if (!name && !row.value) continue;
      if (!name) throw new Error('参数名不能为空。');
      if (row.in === 'path' && row.value === '') throw new Error(`请填写路径参数 ${name}。`);
      const value = testValue(row.value, row.schema || {});
      const key = `${row.in}:${row.in === 'header' ? name.toLowerCase() : name}`;
      if (seen.has(key)) {
        if (row.in !== 'query') throw new Error(`参数 ${name} 不能重复。`);
        const previous = seen.get(key);
        args.query[previous] = [].concat(args.query[previous], value);
      } else {
        args[row.in] ||= Object.create(null);
        args[row.in][name] = value;
        seen.set(key, name);
      }
    }
    return args;
  }
  function testBodyFields(schema = {}) {
    const properties = Object.assign(Object.create(null), schema.properties || {}), required = new Set(schema.required || []);
    const branches = [...(schema.allOf || []), ...(schema.anyOf?.slice(0, 1) || []), ...(schema.oneOf?.slice(0, 1) || [])];
    for (const branch of branches) {
      const fields = testBodyFields(branch);
      Object.assign(properties, fields.properties);
      fields.required.forEach(name => required.add(name));
    }
    return { properties, required: [...required] };
  }
  function testExample(schema = {}, depth = 0) {
    if (depth > 16) return null;
    if (schema.example !== undefined) return schema.example;
    if (schema.default !== undefined) return schema.default;
    if (schema.const !== undefined) return schema.const;
    if (schema.enum?.length) return schema.enum[0];
    const type = Array.isArray(schema.type) ? schema.type.find(type => type !== 'null') : schema.type;
    if (type === 'object' || schema.properties || schema.allOf || schema.anyOf || schema.oneOf) {
      const fields = testBodyFields(schema);
      return Object.fromEntries(Object.entries(fields.properties).filter(([name]) => fields.required.includes(name)).map(([name, field]) => [name, testExample(field, depth + 1)]));
    }
    if (type === 'array') return [testExample(schema.items || {}, depth + 1)];
    if (type === 'integer' || type === 'number') return schema.minimum ?? 1;
    if (type === 'boolean') return true;
    if (type === 'null') return null;
    return '示例值';
  }
  function testPayload(operationId, rows, bodyText, bodyFormat) {
    const payload = { operationId, arguments: testArguments(rows), bodyFormat };
    if (bodyText.trim()) {
      try { JSON.parse(bodyText); } catch { throw new Error('Body 不是有效 JSON，请检查括号、引号和逗号。'); }
      // Send the original JSON string to Go so large integers remain exact.
      payload.body = bodyText;
    }
    return payload;
  }
  root.Api2McpCore = { uid, versionLabel, endpointFor, mcpURLFor, clientConfig, clientRequestHeaders, parseCredentialEntries, testValue, testArguments, testBodyFields, testExample, testPayload };
})(typeof window === 'undefined' ? globalThis : window);
