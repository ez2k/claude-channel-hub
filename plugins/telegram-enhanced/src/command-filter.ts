/**
 * Intercepts bot commands (/memory, /profile, /skills, etc.)
 * and handles them directly without forwarding to Claude.
 */
import type { MiddlewareFn } from "./middleware";

// Commands handled by the plugin directly (not forwarded to Claude)
const HANDLED_COMMANDS = new Set([
  "/memory", "/profile", "/skills", "/search", "/install",
  "/history", "/reset", "/status",
]);

export const commandFilter: MiddlewareFn = async (ctx, next) => {
  const text = ctx.messageText.trim();
  if (!text.startsWith("/")) {
    await next();
    return;
  }

  const cmd = text.split(/\s+/)[0]!.split("@")[0]!.toLowerCase();

  if (HANDLED_COMMANDS.has(cmd)) {
    // Mark as handled — don't forward to Claude
    ctx.handled = true;
    // Actual command handling will be added in later phases
    // For now, just log
    process.stderr.write(`telegram-enhanced: intercepted command ${cmd}\n`);
    return;
  }

  // Unknown command or Telegram-native command — let it through
  await next();
};
