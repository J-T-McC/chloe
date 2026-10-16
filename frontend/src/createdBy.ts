/** "AI (endpoint)" for objects an agent made over MCP, else "". */
export function createdByLabel(by?: string) {
  return by?.startsWith("mcp:") ? `AI (${by.slice(4)})` : "";
}
