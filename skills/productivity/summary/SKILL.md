---
name: summary
description: Summarize long texts, articles, conversations, meeting notes, or documents. Supports multiple formats — bullet points, paragraphs, one-liners. Use when the user asks to summarize, condense, TL;DR, or extract key points.
version: 1.0.0
author: Claude Channel Hub
license: MIT
metadata:
  tags: [Summary, TL;DR, Key-Points, Meeting-Notes, Condensation, Extraction]
---

# Summarization

Extract the essence from long content. Different formats for different needs.

## When to Use

- "Summarize this article"
- "TL;DR"
- "Key points from this text"
- "Summarize this conversation"
- "Meeting notes summary"
- "Give me the highlights"
- User pastes a long block of text

## Summary Formats

### 1. One-Line (TL;DR)

For when brevity is everything. One sentence capturing the core message.

```
TL;DR: [One sentence — the single most important thing]
```

Use when:
- Quick context needed
- Slack/Telegram-style communication
- User explicitly asks for TL;DR

### 2. Bullet Points (Key Points)

Most common format. Best for Telegram.

```
**Key Points:**
- [Most important point]
- [Second most important]
- [Third point]
- [Additional context if needed]

**Bottom line:** [One sentence conclusion]
```

Use when:
- Summarizing articles or reports
- User wants the highlights
- Multiple distinct points to convey

### 3. Executive Summary (Paragraph)

Narrative summary for nuanced content.

```
**Summary:**

[2-3 paragraph summary. First paragraph: what is this about and why it matters.
Second paragraph: key findings or main points. Third paragraph (optional):
implications, conclusions, or next steps.]
```

Use when:
- Content has narrative flow
- Context and relationships matter
- Detailed document summary requested

### 4. Meeting Notes / Conversation Summary

For summarizing discussions.

```
**Meeting Summary — [Date/Topic]**

**Participants:** [if mentioned]

**Discussed:**
- [Topic 1]: [brief summary]
- [Topic 2]: [brief summary]

**Decisions:**
- [Decision made]

**Action Items:**
- [ ] [Person]: [task] by [deadline if mentioned]
- [ ] [Person]: [task]

**Next steps:** [what happens next]
```

Use when:
- Summarizing a conversation log
- Meeting notes requested
- Action items need to be extracted

### 5. Structured Report Summary

For long documents with sections.

```
**[Document Title] — Summary**

**What it is:** [Type of document, source, date]

**Main Argument / Purpose:**
[1-2 sentences]

**Key Sections:**
1. [Section name]: [1 sentence summary]
2. [Section name]: [1 sentence summary]
3. [Section name]: [1 sentence summary]

**Key Data / Findings:**
- [Stat or finding]
- [Stat or finding]

**Conclusions:**
[1-2 sentences]

**Relevance / So what:**
[Why this matters to the reader]
```

## Summarization Process

1. **Read fully** — do not skim before summarizing
2. **Identify the type** — article, conversation, document, code?
3. **Find the core message** — what is the single most important thing?
4. **Extract supporting points** — what 3-5 things support or explain the core?
5. **Note key data** — any specific numbers, names, dates worth preserving
6. **Choose format** — match format to content type and user request
7. **Check length** — summary should be ~10-20% of original, never longer than needed

## Length Guidelines

| Original length | Target summary |
|----------------|----------------|
| 1 paragraph | 1-2 sentences |
| 1 page | 3-5 bullet points |
| Article (5-10 min read) | 5-8 bullets or 2-3 paragraphs |
| Long report (30+ pages) | 1-2 page structured summary |
| Conversation (10+ messages) | Meeting notes format |

For Telegram, keep summaries under ~300 words. Offer more detail if needed.

## What to Include vs. Exclude

**Include:**
- Core argument or thesis
- Key supporting evidence
- Important data, statistics, names
- Decisions and action items
- Conclusions and recommendations

**Exclude:**
- Repetitive examples (keep the best one)
- Background the user already knows
- Tangential details
- Boilerplate/filler language
- Excessive qualifications

## Handling Edge Cases

### Very long content
- Summarize section by section
- Offer a brief TL;DR first, then offer to go deeper

### Technical content
- Preserve technical terms (do not simplify into inaccuracy)
- Explain acronyms on first use
- Keep code examples if they are the key point

### Opinions / editorials
- Note that it is opinion: "The author argues..."
- Separate facts from claims
- Do not present opinion as fact in summary

### Ambiguous requests
- If user pastes text with no instructions, default to bullet-point key points format
- If length or format preference is unclear, ask briefly or provide bullets + offer alternatives
