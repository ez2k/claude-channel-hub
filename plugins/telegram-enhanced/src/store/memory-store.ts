import { mkdirSync, readFileSync, writeFileSync, existsSync } from "fs";
import { join } from "path";
import { randomUUID } from "crypto";
import { homedir } from "os";

export interface Memory {
  id: string;
  userId: string;
  type: "preference" | "context" | "correction" | "fact" | "general";
  content: string;
  tags: string[];
  weight: number;
  accessCount: number;
  createdAt: string;
  updatedAt: string;
}

export class MemoryStore {
  private dataDir: string;

  constructor(dataDir?: string) {
    this.dataDir = dataDir || process.env.HARNESS_DATA_DIR || join(homedir(), ".claude-harness", "data");
  }

  private userDir(userId: string): string {
    const dir = join(this.dataDir, "memory", sanitize(userId));
    mkdirSync(dir, { recursive: true });
    return dir;
  }

  private memoriesPath(userId: string): string {
    return join(this.userDir(userId), "memories.json");
  }

  private loadMemories(userId: string): Memory[] {
    const path = this.memoriesPath(userId);
    if (!existsSync(path)) return [];
    try {
      return JSON.parse(readFileSync(path, "utf-8"));
    } catch {
      return [];
    }
  }

  private saveMemories(userId: string, memories: Memory[]): void {
    writeFileSync(this.memoriesPath(userId), JSON.stringify(memories, null, 2));
  }

  async save(userId: string, memory: Partial<Memory>): Promise<Memory> {
    const memories = this.loadMemories(userId);
    const now = new Date().toISOString();

    const newMem: Memory = {
      id: randomUUID(),
      userId,
      type: memory.type || "general",
      content: memory.content || "",
      tags: memory.tags || [],
      weight: memory.weight ?? 0.5,
      accessCount: 0,
      createdAt: now,
      updatedAt: now,
    };

    // Dedup: if similarity > 0.8 with existing memory of same type, merge
    for (let i = 0; i < memories.length; i++) {
      const existing = memories[i]!;
      if (existing.type === newMem.type && this.similarity(existing.content, newMem.content) > 0.8) {
        existing.content = newMem.content;
        existing.updatedAt = now;
        existing.accessCount++;
        existing.weight = Math.min(1.0, existing.weight + 0.1);
        this.saveMemories(userId, memories);
        return existing;
      }
    }

    memories.push(newMem);
    this.saveMemories(userId, memories);
    return newMem;
  }

  async recall(userId: string, query: string, limit: number = 5): Promise<Memory[]> {
    const memories = this.loadMemories(userId);
    if (memories.length === 0) return [];

    const queryWords = tokenize(query.toLowerCase());
    if (queryWords.length === 0) return [];

    const scored: { memory: Memory; score: number }[] = [];

    for (const mem of memories) {
      const score = this.scoreMemory(mem, queryWords);
      if (score > 0.1) {
        mem.updatedAt = new Date().toISOString();
        mem.accessCount++;
        scored.push({ memory: mem, score });
      }
    }

    scored.sort((a, b) => b.score - a.score);

    const results = scored.slice(0, limit).map((s) => s.memory);

    // Persist access count updates
    this.saveMemories(userId, memories);

    return results;
  }

  async recallByType(userId: string, type: string, limit: number = 10): Promise<Memory[]> {
    const memories = this.loadMemories(userId);
    const filtered = memories.filter((m) => m.type === type);
    filtered.sort((a, b) => {
      const aScore = a.weight * this.recencyFactor(a.updatedAt);
      const bScore = b.weight * this.recencyFactor(b.updatedAt);
      return bScore - aScore;
    });
    return filtered.slice(0, limit);
  }

  async prune(userId: string, maxCount: number = 100): Promise<number> {
    const memories = this.loadMemories(userId);
    if (memories.length <= maxCount) return 0;

    // Score all memories
    const scored = memories.map((m, idx) => ({
      idx,
      score: m.weight * this.recencyFactor(m.updatedAt) * (1 + m.accessCount * 0.1),
    }));

    scored.sort((a, b) => b.score - a.score);

    // Keep top N
    const keepIdxs = new Set(scored.slice(0, maxCount).map((s) => s.idx));
    const pruned = memories.filter((_, i) => keepIdxs.has(i));
    const removed = memories.length - pruned.length;

    this.saveMemories(userId, pruned);
    return removed;
  }

  async stats(userId: string): Promise<Record<string, number>> {
    const memories = this.loadMemories(userId);
    const result: Record<string, number> = { total: 0 };
    for (const m of memories) {
      result.total!++;
      result[m.type] = (result[m.type] || 0) + 1;
    }
    return result;
  }

  private scoreMemory(mem: Memory, queryWords: string[]): number {
    const contentWords = tokenize(mem.content.toLowerCase());
    const tagStr = mem.tags.join(" ").toLowerCase();

    let matchCount = 0;
    for (const qw of queryWords) {
      for (const cw of contentWords) {
        if (cw.includes(qw) || qw.includes(cw)) {
          matchCount++;
          break;
        }
      }
      if (tagStr.includes(qw)) {
        matchCount++;
      }
    }

    const overlapScore = matchCount / queryWords.length;
    return overlapScore * mem.weight * this.recencyFactor(mem.updatedAt);
  }

  private similarity(a: string, b: string): number {
    const aWords = tokenize(a.toLowerCase());
    const bWords = tokenize(b.toLowerCase());
    if (aWords.length === 0 || bWords.length === 0) return 0;

    let matches = 0;
    for (const aw of aWords) {
      for (const bw of bWords) {
        if (aw === bw) {
          matches++;
          break;
        }
      }
    }
    return matches / Math.max(aWords.length, bWords.length);
  }

  private recencyFactor(dateStr: string): number {
    const hours = (Date.now() - new Date(dateStr).getTime()) / (1000 * 60 * 60);
    if (hours < 1) return 1.0;
    if (hours < 24) return 0.9;
    if (hours < 168) return 0.7; // 1 week
    if (hours < 720) return 0.5; // 1 month
    return 0.3;
  }
}

function tokenize(s: string): string[] {
  return s
    .split(/\s+/)
    .map((w) => w.replace(/^[.,!?;:"'()\[\]{}]+|[.,!?;:"'()\[\]{}]+$/g, ""))
    .filter((w) => w.length > 1);
}

function sanitize(s: string): string {
  return s.replace(/[/\\]/g, "_").replace(/\.\./g, "_");
}
