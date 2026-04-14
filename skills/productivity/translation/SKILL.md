---
name: translation
description: Translate text between languages with context-awareness. Supports Korean, English, Japanese, Chinese, and more. Preserves formatting, technical terms, and tone. Use when the user asks to translate, localize, or explains something in another language.
version: 1.0.0
author: Claude Channel Hub
license: MIT
metadata:
  tags: [Translation, Korean, English, Japanese, Chinese, Localization, Language]
---

# Translation

Context-aware translation that preserves meaning, formatting, and technical terminology.

## When to Use

- User sends text in a foreign language
- "Translate this to Korean/English/Japanese/Chinese"
- "What does this mean in English?"
- "Help me write this in [language]"
- Localization of technical content or UI strings

## Core Languages

| Language | Code | Notes |
|----------|------|-------|
| Korean | ko | Formal (습니다체) vs informal (해요체) distinction matters |
| English | en | US vs UK spelling when relevant |
| Japanese | ja | Keigo (polite) vs casual register |
| Chinese (Simplified) | zh-CN | Mainland standard |
| Chinese (Traditional) | zh-TW | Taiwan/Hong Kong usage |

## Translation Principles

### 1. Context First

Before translating, identify:
- **Domain**: technical, legal, casual conversation, marketing, UI
- **Register**: formal, semi-formal, or casual
- **Audience**: general public, developers, academics, children
- **Purpose**: information, persuasion, instruction

### 2. Preserve Formatting

- Keep markdown formatting (bold, italic, code blocks, lists)
- Preserve line breaks and paragraph structure
- Keep code snippets untranslated; translate comments only
- Maintain numbered/bulleted list structure

### 3. Technical Terms

- Keep widely-used technical terms in original language when appropriate
  - "API", "server", "deploy", "commit" — often kept as-is in Korean/Japanese tech writing
- When translating technical docs, add original term in parentheses on first use
  - Example: 컨테이너(container)
- Do NOT translate: proper nouns, brand names, code identifiers, URLs

### 4. Korean-Specific Guidelines

**Formality levels:**
- Formal (공식적): 습니다/습니까 endings — for official documents, business
- Semi-formal (일반적): 해요/해요 endings — most conversational and Telegram use
- Casual (반말): 해/야 endings — close friends only

**Common pitfalls:**
- English passive voice → Korean often uses active or different construction
- English articles (a/the) → not used in Korean, adjust surrounding context
- Long English sentences → often better split into shorter Korean sentences

### 5. Japanese-Specific Guidelines

**Politeness:**
- Desu/masu form for general use
- Plain form for casual/informal
- Keigo (honorific) for business/formal

**Text direction:** Left-to-right in modern digital contexts (same as English)

### 6. Chinese-Specific Guidelines

- Simplified (简体) for mainland China audiences
- Traditional (繁體) for Taiwan, Hong Kong, Macao
- When target unclear, ask or default to Simplified

## Output Format

### Single translation
```
**[Target Language] Translation:**
[translated text]

---
*Note: [any translation choices explained, e.g., "used formal register" or "kept 'API' untranslated as is standard in Korean tech writing"]*
```

### Bilingual (side-by-side for short texts)
```
**Original (EN):** Hello, how are you?
**Translation (KO):** 안녕하세요, 어떻게 지내세요?
```

### Technical content with notes
```
[Translation]

**Translation notes:**
- "[term]" kept in English — standard usage in Korean tech writing
- Used formal register (합쇼체) appropriate for documentation
- "[phrase]" translated as "[choice]" rather than literal "[alternative]" for natural flow
```

## Detecting Source Language

If no source language is specified, detect it automatically:
1. Identify the script (Hangul = Korean, Hiragana/Katakana/Kanji = Japanese, etc.)
2. Confirm: "This appears to be [language]. Translating to [target]..."
3. If ambiguous between zh-CN and zh-TW, ask or note the assumption

## Handling Untranslatable Concepts

Some concepts don't translate directly. Options:
1. **Transliteration**: keep the sound, e.g., 눈치 → nunchi (with explanation)
2. **Explanation**: translate the concept rather than the word
3. **Loan word**: use the foreign word in the target language
4. **Footnote**: translate + add "(lit. [literal meaning])"

Always be transparent when a word is culturally specific or hard to translate.

## Quick Reference

| Request | Action |
|---------|--------|
| "translate this" | Detect source, translate to user's primary language |
| "what does X mean" | Translate + explain cultural/contextual meaning |
| "translate to Korean" | Use semi-formal (해요체) unless specified |
| "formal Korean" | Use 합쇼체/습니다체 |
| "translate my code comments" | Translate comments only, preserve code |
| "localize this UI" | Adapt text for natural feel, not just literal translation |
