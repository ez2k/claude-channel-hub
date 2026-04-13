/**
 * Middleware chain for processing inbound Telegram messages
 * before they reach Claude Code via handleInbound().
 */

export interface ContentPart {
  type: "text" | "image";
  text?: string;
  image_path?: string;
}

export interface InboundContext {
  /** Original message text */
  messageText: string;
  /** Telegram metadata */
  meta: {
    chat_id: string;
    user_id: string;
    username: string;
    message_id?: string;
    ts: string;
  };
  /** Additional content parts injected by middlewares (memories, profile, etc.) */
  contentParts: ContentPart[];
  /** Channel config from HARNESS_CHANNELS_CONFIG (if matched) */
  channelConfig?: ChannelConfig | null;
  /** Set to true by a middleware to prevent forwarding to Claude */
  handled: boolean;
}

export interface ChannelConfig {
  id: string;
  bot: string;
  name: string;
  match: { type: string; chat_ids?: string[]; user_ids?: string[]; topic_ids?: string[] };
  model: string;
  system_prompt: string;
  data_dir: string;
}

export type MiddlewareFn = (ctx: InboundContext, next: () => Promise<void>) => Promise<void>;

export class MiddlewareChain {
  private middlewares: MiddlewareFn[] = [];

  use(mw: MiddlewareFn): void {
    this.middlewares.push(mw);
  }

  async run(ctx: InboundContext): Promise<void> {
    let index = 0;
    const next = async (): Promise<void> => {
      if (index < this.middlewares.length) {
        const mw = this.middlewares[index++]!;
        await mw(ctx, next);
      }
    };
    await next();
  }
}
