import { mkdirSync, readFileSync, writeFileSync, existsSync } from "fs";
import { join } from "path";
import { homedir } from "os";

export interface Profile {
  userId: string;
  language: string;
  name: string;
  sessionCount: number;
  totalMessages: number;
  style: {
    formality: string;
    detailLevel: string;
    techLevel: string;
  };
  topics: Record<string, number>;
  expertise: string[];
  updatedAt: string;
}

export class ProfileStore {
  private dataDir: string;

  constructor(dataDir?: string) {
    this.dataDir = dataDir || process.env.HARNESS_DATA_DIR || join(homedir(), ".claude-channel-hub", "data");
  }

  private profilePath(userId: string): string {
    const dir = join(this.dataDir, "profiles", sanitize(userId));
    mkdirSync(dir, { recursive: true });
    return join(dir, "profile.json");
  }

  async get(userId: string): Promise<Profile | null> {
    const path = this.profilePath(userId);
    if (!existsSync(path)) return null;
    try {
      return JSON.parse(readFileSync(path, "utf-8"));
    } catch {
      return null;
    }
  }

  private createDefault(userId: string): Profile {
    return {
      userId,
      language: "auto",
      name: "",
      sessionCount: 0,
      totalMessages: 0,
      style: {
        formality: "mixed",
        detailLevel: "mixed",
        techLevel: "intermediate",
      },
      topics: {},
      expertise: [],
      updatedAt: new Date().toISOString(),
    };
  }

  async observeMessage(userId: string, text: string): Promise<void> {
    let profile = await this.get(userId);
    if (!profile) {
      profile = this.createDefault(userId);
    }

    profile.totalMessages++;
    profile.updatedAt = new Date().toISOString();

    // Detect language
    if (profile.language === "auto" || profile.language === "") {
      profile.language = detectLanguage(text);
    }

    // Extract topics
    for (const topic of extractTopics(text)) {
      profile.topics[topic] = (profile.topics[topic] || 0) + 1;
    }

    // Update style
    updateStyle(profile, text);

    // Persist
    writeFileSync(this.profilePath(userId), JSON.stringify(profile, null, 2));
  }

  async update(userId: string, updates: Partial<Profile>): Promise<Profile> {
    let profile = await this.get(userId);
    if (!profile) {
      profile = this.createDefault(userId);
    }

    if (updates.name !== undefined) profile.name = updates.name;
    if (updates.language !== undefined) profile.language = updates.language;
    if (updates.expertise !== undefined) profile.expertise = updates.expertise;
    if (updates.style !== undefined) {
      if (updates.style.formality) profile.style.formality = updates.style.formality;
      if (updates.style.detailLevel) profile.style.detailLevel = updates.style.detailLevel;
      if (updates.style.techLevel) profile.style.techLevel = updates.style.techLevel;
    }
    profile.updatedAt = new Date().toISOString();

    writeFileSync(this.profilePath(userId), JSON.stringify(profile, null, 2));
    return profile;
  }

  formatForPrompt(profile: Profile): string {
    if (profile.totalMessages < 3) return "";

    const lines: string[] = ["\n<user_profile>"];

    if (profile.name) {
      lines.push(`Name: ${profile.name}`);
    }
    if (profile.language !== "auto" && profile.language !== "") {
      lines.push(`Preferred language: ${profile.language}`);
    }

    lines.push(
      `Communication style: ${profile.style.formality} formality, ${profile.style.detailLevel} detail, ${profile.style.techLevel} technical level`
    );

    if (profile.expertise.length > 0) {
      lines.push(`Known expertise: ${profile.expertise.join(", ")}`);
    }

    // Top 5 topics
    const topTopics = topN(profile.topics, 5);
    if (topTopics.length > 0) {
      lines.push(`Frequent topics: ${topTopics.join(", ")}`);
    }

    lines.push(`Sessions: ${profile.sessionCount}, Messages: ${profile.totalMessages}`);
    lines.push("</user_profile>");
    lines.push("Adapt your responses to match this user's style and expertise level.");

    return lines.join("\n");
  }
}

function detectLanguage(text: string): string {
  let koreanCount = 0;
  let japaneseCount = 0;
  let totalChars = 0;

  for (const ch of text) {
    const code = ch.codePointAt(0)!;
    totalChars++;
    // Hangul syllables
    if (code >= 0xac00 && code <= 0xd7af) {
      koreanCount++;
    }
    // Hiragana + Katakana
    if ((code >= 0x3040 && code <= 0x309f) || (code >= 0x30a0 && code <= 0x30ff)) {
      japaneseCount++;
    }
  }

  if (totalChars > 0 && koreanCount / totalChars > 0.2) return "ko";
  if (totalChars > 0 && japaneseCount / totalChars > 0.1) return "ja";
  return "en";
}

function extractTopics(text: string): string[] {
  const keywords: Record<string, string[]> = {
    coding: ["code", "programming", "function", "bug", "variable", "class"],
    devops: ["docker", "kubernetes", "k8s", "deploy", "ci/cd", "server"],
    ai: ["ai", "ml", "model", "llm", "gpt", "claude", "agent"],
    database: ["db", "sql", "database", "query", "postgres", "mysql"],
    web: ["html", "css", "react", "frontend", "backend", "api"],
    mobile: ["ios", "android", "app", "mobile"],
    security: ["security", "auth", "token", "encryption"],
    automation: ["automation", "script", "workflow", "cron"],
  };

  const lower = text.toLowerCase();
  const found: string[] = [];
  for (const [topic, words] of Object.entries(keywords)) {
    for (const w of words) {
      if (lower.includes(w)) {
        found.push(topic);
        break;
      }
    }
  }
  return found;
}

function updateStyle(profile: Profile, text: string): void {
  const lower = text.toLowerCase();

  // Formality detection (includes Korean markers from Go source)
  if (lower.includes("please") || lower.includes("would you") || lower.includes("kindly")) {
    profile.style.formality = "formal";
  } else if (lower.includes("lol") || lower.includes("haha") || lower.includes("gonna")) {
    profile.style.formality = "casual";
  }

  // Tech level detection
  const techTerms = ["api", "sdk", "docker", "kubernetes", "goroutine", "mutex", "concurrency", "microservice"];
  let techCount = 0;
  for (const t of techTerms) {
    if (lower.includes(t)) techCount++;
  }
  if (techCount >= 2) {
    profile.style.techLevel = "expert";
  } else if (techCount >= 1) {
    profile.style.techLevel = "intermediate";
  }
}

function topN(m: Record<string, number>, n: number): string[] {
  return Object.entries(m)
    .sort((a, b) => b[1] - a[1])
    .slice(0, n)
    .map(([k]) => k);
}

function sanitize(s: string): string {
  return s.replace(/[/\\]/g, "_").replace(/\.\./g, "_");
}
