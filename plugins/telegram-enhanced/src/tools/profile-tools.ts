import { ProfileStore } from "../store/profile-store.js";

export const profileTools = [
  {
    name: "profile_get",
    description: "Get user profile (language, style, topics, expertise)",
    inputSchema: {
      type: "object" as const,
      properties: {
        user_id: { type: "string", description: "User ID to get profile for" },
      },
      required: ["user_id"],
    },
  },
  {
    name: "profile_update",
    description: "Update user profile fields",
    inputSchema: {
      type: "object" as const,
      properties: {
        user_id: { type: "string", description: "User ID to update profile for" },
        name: { type: "string", description: "Display name" },
        language: { type: "string", description: "Preferred language (ko, en, ja)" },
        expertise: { type: "array", items: { type: "string" }, description: "Known expertise areas" },
      },
      required: ["user_id"],
    },
  },
];

export async function handleProfileTool(
  name: string,
  args: Record<string, unknown>,
  store: ProfileStore
): Promise<unknown> {
  switch (name) {
    case "profile_get": {
      const profile = await store.get(args.user_id as string);
      if (!profile) {
        return { message: "No profile found for this user yet." };
      }
      return profile;
    }
    case "profile_update": {
      const updates: Record<string, unknown> = {};
      if (args.name !== undefined) updates.name = args.name;
      if (args.language !== undefined) updates.language = args.language;
      if (args.expertise !== undefined) updates.expertise = args.expertise;
      return store.update(args.user_id as string, updates as any);
    }
    default:
      throw new Error(`Unknown profile tool: ${name}`);
  }
}
