import { MemoryStore } from "../store/memory-store.js";

export const memoryTools = [
  {
    name: "memory_recall",
    description: "Search user memories by query",
    inputSchema: {
      type: "object" as const,
      properties: {
        query: { type: "string", description: "Search query to match against memories" },
        user_id: { type: "string", description: "User ID to recall memories for" },
        type: {
          type: "string",
          enum: ["preference", "context", "correction", "fact", "general"],
          description: "Optional: filter by memory type",
        },
        limit: { type: "number", description: "Max results to return (default 5)" },
      },
      required: ["query", "user_id"],
    },
  },
  {
    name: "memory_save",
    description: "Save a memory for a user",
    inputSchema: {
      type: "object" as const,
      properties: {
        user_id: { type: "string", description: "User ID to save memory for" },
        content: { type: "string", description: "Memory content to save" },
        type: {
          type: "string",
          enum: ["preference", "context", "correction", "fact", "general"],
          description: "Memory type (default: general)",
        },
        tags: { type: "array", items: { type: "string" }, description: "Optional tags for categorization" },
      },
      required: ["user_id", "content"],
    },
  },
  {
    name: "memory_stats",
    description: "Get memory statistics for a user",
    inputSchema: {
      type: "object" as const,
      properties: {
        user_id: { type: "string", description: "User ID to get stats for" },
      },
      required: ["user_id"],
    },
  },
];

export async function handleMemoryTool(
  name: string,
  args: Record<string, unknown>,
  store: MemoryStore
): Promise<unknown> {
  switch (name) {
    case "memory_recall": {
      const memories = args.type
        ? await store.recallByType(args.user_id as string, args.type as string, (args.limit as number) || 5)
        : await store.recall(args.user_id as string, args.query as string, (args.limit as number) || 5);
      return memories;
    }
    case "memory_save": {
      return store.save(args.user_id as string, {
        content: args.content as string,
        type: (args.type as Memory["type"]) || "general",
        tags: (args.tags as string[]) || [],
      });
    }
    case "memory_stats": {
      return store.stats(args.user_id as string);
    }
    default:
      throw new Error(`Unknown memory tool: ${name}`);
  }
}

// Re-export for convenience
type Memory = import("../store/memory-store.js").Memory;
