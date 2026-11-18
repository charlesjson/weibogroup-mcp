import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { mkdtemp, readdir, readFile, rm, writeFile } from 'node:fs/promises'
import { createServer } from 'node:http'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { test } from 'node:test'
import { Client } from '@modelcontextprotocol/sdk/client/index.js'
import { StdioClientTransport } from '@modelcontextprotocol/sdk/client/stdio.js'
const emptyAccounts = { accounts: [], nextCursor: null }

test('the release executable discovers and calls tools through stdio', async t => {
  const dir = await mkdtemp(join(tmpdir(), 'weibogroup-mcp-'))
  const connection = join(dir, 'connection.json')
  const exportDir = join(dir, 'downloads')
  const token = 'wg_ai_' + 'x'.repeat(43)
  const id = '12345678-1234-4234-8234-123456789abc'
  let bytes = Buffer.from('name,value\nexample,1\n')
  const image = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jS1kAAAAASUVORK5CYII=', 'base64')
  const hash = data => createHash('sha256').update(data).digest('hex')
  const key = { accountId: 'a', groupId: 'g', messageId: 'm', assetType: 'image', assetKey: 'asset' }
  const job = {
    id, kind: 'messages', accountId: 'a', groupId: 'g', format: 'csv', status: 'ready',
    createdAt: 1, finishedAt: 2, expiresAt: 9999999999999, filename: '../test.csv',
    contentType: 'text/csv', size: bytes.length, errorCode: null,
    result: { messageCount: 1, rowLimit: 10, truncated: false },
  }
  let shortDownload = false
  const requests = []
  const api = createServer((req, res) => {
    requests.push(req.url)
    if (req.headers.authorization !== 'Bearer ' + token) return res.writeHead(401).end()
    const path = new URL(req.url, 'http://localhost').pathname
    if (path === '/api/v1/accounts') {
      return res.writeHead(200, { 'content-type': 'application/json' }).end(JSON.stringify(
        emptyAccounts))
    }
    if (path === '/api/v1/messages/image') {
      return res.writeHead(200, {
        'content-type': 'image/png', 'x-weibogroup-image-sha256': hash(image), 'x-weibogroup-image-count': '1',
        'x-weibogroup-image-preview': '0', 'x-weibogroup-image-binding': hash(JSON.stringify([...Object.values(key), 0])),
      }).end(image)
    }
    if (path === '/api/v1/exports/' + id)
      return res.writeHead(200, { 'content-type': 'application/json' }).end(JSON.stringify({ job }))
    if (path === '/api/v1/exports/' + id + '/download') {
      res.writeHead(200, { 'content-type': job.contentType })
      res.write(bytes.subarray(0, 2))
      return res.end(shortDownload ? undefined : bytes.subarray(2))
    }
    res.writeHead(404).end()
  })
  await new Promise(resolve => api.listen(0, '127.0.0.1', resolve))
  const client = new Client({ name: 'integration-test', version: '1' })
  t.after(async () => {
    await client.close()
    await new Promise(resolve => api.close(resolve))
    await rm(dir, { recursive: true, force: true })
  })
  await client.connect(new StdioClientTransport({
    command: resolve('dist/weibogroup-mcp'), cwd: dir,
    env: { WEIBOGROUP_CONNECTION_FILE: connection, WEIBOGROUP_EXPORT_DIR: exportDir },
  }))
  const { tools } = await client.listTools()
  assert.equal(new Set(tools.map(tool => tool.name)).size, 45)
  assert.ok(tools.every(tool => tool.name.startsWith('weibo_')))
  assert.ok(!tools.some(tool => /sendGroup|recallGroup/.test(tool.name)))
  assert.ok(!tools.some(tool => tool.name === 'weibo_getWordCloud'))
  const createUser = tools.find(tool => tool.name === 'weibo_createUserExport')
  assert.deepEqual(createUser.inputSchema.properties.format.enum, ['zip'])
  const call = (name, args = {}) => client.callTool({ name: 'weibo_' + name, arguments: args })
  assert.equal((await call('listAccounts')).isError, true)
  await writeFile(connection, JSON.stringify({ baseUrl: 'http://127.0.0.1:' + api.address().port, apiToken: token }), { mode: 0o600 })
  assert.ok(!(await call('listAccounts')).isError)
  assert.equal((await call('searchMessages', { limit: 21 })).isError, true)
  assert.equal((await call('downloadExport', { id: '../bad' })).isError, true)
  assert.equal((await call('readMessageImage', {})).isError, true)
  assert.equal((await call('createUserExport', { accountId: 'a', groupId: 'g', senderUid: '123', format: 'pdf' })).isError, true)
  assert.deepEqual(requests, ['/api/v1/accounts'])
  const picture = await call('readMessageImage', key)
  assert.deepEqual(picture.content[1], { type: 'image', mimeType: 'image/png', data: image.toString('base64') })
  const file = (await call('downloadExport', { id })).structuredContent
  assert.deepEqual(await readFile(file.path), bytes)
  assert.ok(file.path.startsWith(join(exportDir, 'export-')))
  assert.equal(file.coverage.messageCount, 1)
  const legacyBytes = Buffer.from('%PDF-1.4\nfixture')
  Object.assign(job, { kind: 'user', format: 'pdf', filename: 'legacy.pdf', contentType: 'application/pdf', size: legacyBytes.length })
  bytes = legacyBytes
  const legacy = (await call('downloadExport', { id })).structuredContent
  assert.equal(legacy.filename, 'legacy.pdf')
  assert.deepEqual(await readFile(legacy.path), legacyBytes)
  const previous = await readdir(exportDir)
  shortDownload = true
  assert.equal((await call('downloadExport', { id })).isError, true)
  assert.deepEqual(await readdir(exportDir), previous)
})

test('flags and environment bind each API endpoint to its own key', async t => {
  const dir = await mkdtemp(join(tmpdir(), 'weibogroup-credentials-'))
  const seen = []
  const api = createServer((req, res) => {
    seen.push([req.url, req.headers.authorization])
    res.writeHead(200, { 'content-type': 'application/json' }).end(JSON.stringify(
      emptyAccounts))
  })
  await new Promise(resolve => api.listen(0, '127.0.0.1', resolve))
  t.after(async () => {
    await new Promise(resolve => api.close(resolve))
    await rm(dir, { recursive: true, force: true })
  })
  const base = 'http://127.0.0.1:' + api.address().port
  const token = ch => 'wg_ai_' + ch.repeat(43)
  const env = { WEIBOGROUP_CONNECTION_FILE: join(dir, 'missing.json'),
    WEIBOGROUP_API_ENDPOINT: base + '/one/api/v1/', WEIBOGROUP_API_KEY: token('a') }
  const command = resolve('dist/weibogroup-mcp')
  for (const args of [[], ['--api-endpoint', base + '/two', '--api-key', token('b')]]) {
    const client = new Client({ name: 'config-test', version: '1' })
    try {
      await client.connect(new StdioClientTransport({ command, args, env }))
      const result = await client.callTool({ name: 'weibo_listAccounts', arguments: {} })
      assert.ok(!result.isError)
    } finally { await client.close() }
  }
  assert.deepEqual(seen, [['/one/api/v1/accounts', 'Bearer ' + token('a')], ['/two/api/v1/accounts', 'Bearer ' + token('b')]])
  const { spawnSync } = await import('node:child_process')
  for (const [args, extraEnv] of [
    [['--api-endpoint', base], env],
    [[], { WEIBOGROUP_API_KEY: token('a') }],
    [['--api-endpoint', 'http://archive.example', '--api-key', token('b')], {}],
  ]) {
    const failed = spawnSync(command, args, { env: extraEnv, cwd: dir, encoding: 'utf8' })
    assert.equal(failed.status, 2)
    assert.equal(failed.stdout, '')
    assert.ok(!failed.stderr.includes('wg_ai_'))
  }
})
