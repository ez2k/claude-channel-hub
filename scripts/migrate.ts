#!/usr/bin/env bun
/**
 * Data migration: Go Harness → Claude Channel Hub
 *
 * Usage: bun scripts/migrate.ts --from ./data --to ~/.claude-channel-hub/data
 */
import { readdirSync, readFileSync, writeFileSync, mkdirSync, existsSync } from "fs"
import { join, basename } from "path"

const args = process.argv.slice(2)
let fromDir = "./data"
let toDir = join(process.env.HOME || "/root", ".claude-channel-hub", "data")

for (let i = 0; i < args.length; i++) {
  if (args[i] === "--from" && args[i + 1]) fromDir = args[++i]
  if (args[i] === "--to" && args[i + 1]) toDir = args[++i]
}

console.log(`Migration: ${fromDir} → ${toDir}`)

let migratedMemories = 0
let migratedProfiles = 0
let migratedSkills = 0

// 1. Migrate profiles
// Go format: data/_profiles/tg-main_69677888.json
// New format: toDir/profiles/69677888/profile.json
const profilesDir = join(fromDir, "_profiles")
if (existsSync(profilesDir)) {
  for (const file of readdirSync(profilesDir)) {
    if (!file.endsWith(".json")) continue
    try {
      const raw = JSON.parse(readFileSync(join(profilesDir, file), "utf8"))
      // Extract user_id from filename: tg-main_69677888.json → 69677888
      // Prefer the user_id field in the JSON if present
      const parts = basename(file, ".json").split("_")
      const userId = (raw.user_id || raw.userId || parts[parts.length - 1]) as string

      const outDir = join(toDir, "profiles", userId)
      mkdirSync(outDir, { recursive: true })

      // Map Go profile fields to new format
      const profile = {
        userId,
        language: raw.language || "ko",
        name: raw.name || "",
        sessionCount: raw.session_count ?? raw.sessionCount ?? 0,
        totalMessages: raw.total_messages ?? raw.totalMessages ?? 0,
        style: {
          formality: raw.style?.formality || "casual",
          detailLevel: raw.style?.detail_level || raw.style?.detailLevel || "detailed",
          techLevel: raw.style?.tech_level || raw.style?.techLevel || "intermediate",
        },
        topics: raw.topics || {},
        expertise: raw.expertise || [],
        lastSeen: raw.last_seen || raw.lastSeen || null,
        updatedAt: new Date().toISOString(),
      }

      writeFileSync(join(outDir, "profile.json"), JSON.stringify(profile, null, 2))
      migratedProfiles++
      console.log(`  ✅ Profile: ${file} → profiles/${userId}/profile.json`)
    } catch (e) {
      console.log(`  ⚠️  Profile: ${file} — ${e}`)
    }
  }
} else {
  console.log(`  ⚠️  Profiles dir not found: ${profilesDir}`)
}

// 2. Migrate memories
// Go format: data/_memory/{user_id}.json (array or {memories:[...]})
// New format: toDir/memory/{user_id}/memories.json
const memoryDir = join(fromDir, "_memory")
if (existsSync(memoryDir)) {
  const memFiles = readdirSync(memoryDir).filter(f => f.endsWith(".json"))
  if (memFiles.length === 0) {
    console.log(`  ℹ️  No memory files found in ${memoryDir}`)
  }
  for (const file of memFiles) {
    try {
      const raw = JSON.parse(readFileSync(join(memoryDir, file), "utf8"))
      // Group by user_id
      const byUser: Record<string, object[]> = {}
      const items: object[] = Array.isArray(raw) ? raw : (raw.memories || [])
      for (const m of items as Record<string, unknown>[]) {
        const uid = (m.user_id || m.userId || basename(file, ".json") || "unknown") as string
        if (!byUser[uid]) byUser[uid] = []
        byUser[uid].push({
          id: m.id || crypto.randomUUID(),
          userId: uid,
          type: m.type || "general",
          content: m.content || "",
          tags: m.tags || [],
          weight: m.weight ?? 0.5,
          accessCount: m.access_count ?? m.accessCount ?? 0,
          createdAt: m.created_at || m.createdAt || new Date().toISOString(),
          updatedAt: m.updated_at || m.updatedAt || new Date().toISOString(),
        })
      }

      for (const [userId, memories] of Object.entries(byUser)) {
        const outDir = join(toDir, "memory", userId)
        mkdirSync(outDir, { recursive: true })
        writeFileSync(join(outDir, "memories.json"), JSON.stringify(memories, null, 2))
        migratedMemories += memories.length
        console.log(`  ✅ Memory: ${memories.length} entries → memory/${userId}/memories.json`)
      }
    } catch (e) {
      console.log(`  ⚠️  Memory: ${file} — ${e}`)
    }
  }
} else {
  console.log(`  ⚠️  Memory dir not found: ${memoryDir}`)
}

// 3. Migrate learned skills
// Go format: skills/_learned/*.md  (relative to project root, one level up from data/)
// New format: toDir/skills/_learned/*.md (just copy)
// Try both relative-to-fromDir/../skills/_learned and an explicit path
const learnedCandidates = [
  join(fromDir, "..", "skills", "_learned"),
  join(fromDir, "skills", "_learned"),
]

let learnedDir: string | null = null
for (const candidate of learnedCandidates) {
  if (existsSync(candidate)) {
    learnedDir = candidate
    break
  }
}

if (learnedDir) {
  const outDir = join(toDir, "skills", "_learned")
  mkdirSync(outDir, { recursive: true })
  for (const file of readdirSync(learnedDir)) {
    if (!file.endsWith(".md")) continue
    try {
      const content = readFileSync(join(learnedDir, file), "utf8")
      writeFileSync(join(outDir, file), content)
      migratedSkills++
      console.log(`  ✅ Skill: ${file}`)
    } catch (e) {
      console.log(`  ⚠️  Skill: ${file} — ${e}`)
    }
  }
} else {
  console.log(`  ℹ️  No learned skills dir found (checked: ${learnedCandidates.join(", ")})`)
}

console.log(`\n📊 Migration complete:`)
console.log(`   Profiles: ${migratedProfiles}`)
console.log(`   Memories: ${migratedMemories}`)
console.log(`   Skills:   ${migratedSkills}`)
