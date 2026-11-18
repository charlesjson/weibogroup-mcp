// Run against a built checkout of charlesjson/dsh-weibogroup v0.6.3.
import { resolve } from 'node:path'
import { pathToFileURL } from 'node:url'
import { writeFile } from 'node:fs/promises'
const dist = pathToFileURL(resolve(process.argv[2], 'dist') + '/')
const { archiveToolCatalog } = await import(new URL('api/catalog.js', dist))
const { messagePageTool } = await import(new URL('api/message-pages.js', dist))
const { imageQuerySchema } = await import(new URL('vendor/image-payload.js', dist))
const operations = [...archiveToolCatalog().operations.values()].map(op => ({
  method: op.method, route: op.routePath, bindings: op.arguments,
  body: op.hasRequestBody, response: op.tool.outputSchema, tool: messagePageTool(op.tool),
}))
operations.push({
  tool: { name: 'readMessageImage', description: 'Read one archived picture as MCP image content using the exact identifiers from its message assets.', inputSchema: imageQuerySchema, annotations: { readOnlyHint: true } },
}, {
  tool: { name: 'downloadExport', description: 'Save a ready export to a new local file. Returns path, bytes and coverage.', inputSchema: { type: 'object', required: ['id'], additionalProperties: false, properties: { id: { type: 'string', format: 'uuid' } } }, annotations: { readOnlyHint: false, destructiveHint: false, idempotentHint: false } },
})
await writeFile(new URL('../catalog.json', import.meta.url), JSON.stringify(operations, null, 2) + '\n')
