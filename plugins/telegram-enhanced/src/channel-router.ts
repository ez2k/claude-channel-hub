/**
 * Routes incoming messages to the correct channel based on HARNESS_CHANNELS_CONFIG.
 */
import type { ChannelConfig, MiddlewareFn } from "./middleware";

let channelConfigs: ChannelConfig[] = [];

export function loadChannelConfigs(): void {
  const raw = process.env.HARNESS_CHANNELS_CONFIG;
  if (!raw) return;
  try {
    channelConfigs = JSON.parse(raw);
  } catch {
    process.stderr.write("telegram-enhanced: failed to parse HARNESS_CHANNELS_CONFIG\n");
  }
}

export function matchChannel(chatId: string, userId: string): ChannelConfig | null {
  // Try specific matches first, then default
  for (const ch of channelConfigs) {
    if (ch.match.type === "group" && ch.match.chat_ids?.includes(chatId)) return ch;
    if (ch.match.type === "user" && ch.match.user_ids?.includes(userId)) return ch;
  }
  // Fall back to default
  for (const ch of channelConfigs) {
    if (ch.match.type === "default") return ch;
  }
  return null;
}

export const channelRouter: MiddlewareFn = async (ctx, next) => {
  ctx.channelConfig = matchChannel(ctx.meta.chat_id, ctx.meta.user_id);
  await next();
};
