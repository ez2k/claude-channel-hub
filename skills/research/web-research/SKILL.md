---
name: web-research
description: Systematic web research for information gathering, fact-checking, and topic deep-dives. Use when the user asks to research a topic, find information online, compare options, or gather sources.
version: 1.0.0
author: Claude Channel Hub
license: MIT
metadata:
  tags: [Research, Web, Search, Sources, Synthesis]
---

# Web Research

Systematic information gathering from the web — search, evaluate, synthesize, cite.

## When to Use

- "Research X for me"
- "Find information about Y"
- "What's the latest on Z"
- "Compare A vs B"
- "Fact-check this claim"
- "Gather sources on topic"

## Research Process

### 1. Clarify the Goal

Before searching, understand:
- What specifically does the user need?
- What depth is required? (quick overview vs. deep dive)
- What format for results? (bullets, report, comparison table)
- Any specific sources to include or avoid?

### 2. Search Strategy

Start broad, then narrow:

```
# Broad query first
web_search("topic overview")

# Then specific queries
web_search("topic specific aspect 2024")
web_search("topic comparison alternative")
web_search("topic expert opinion site:reddit.com OR site:hackernews.com")
```

**Query techniques:**
- Use quotes for exact phrases: `"chain of thought prompting"`
- Use `site:` to target specific domains
- Use `-term` to exclude results
- Add year for recency: `topic 2024 2025`
- Use `filetype:pdf` for documents

### 3. Source Evaluation

Rate each source before using it:

| Criterion | Questions to ask |
|-----------|-----------------|
| **Authority** | Who wrote it? What are their credentials? |
| **Accuracy** | Are claims supported? Can you verify facts? |
| **Currency** | When was it published/updated? Is it still relevant? |
| **Purpose** | Is it informational, opinion, or promotional? |
| **Coverage** | Is it comprehensive or partial? |

**Trusted source types by topic:**
- Technical: official docs, GitHub, Stack Overflow, HN
- Academic: arXiv, Google Scholar, PubMed
- News: major outlets with editorial standards
- Statistics: government data, official reports
- Products: official sites + independent reviews

### 4. Extracting Content

```
# Read a specific page
web_fetch("https://example.com/article")

# For PDFs
web_fetch("https://example.com/report.pdf")
```

Read the most relevant 2-3 sources in full rather than skimming many.

### 5. Synthesis

After gathering information:

1. **Identify key themes** — what do multiple sources agree on?
2. **Note disagreements** — where do sources conflict?
3. **Flag uncertainties** — what is unclear or contested?
4. **Highlight recency** — what is the latest development?

## Output Formats

### Quick Summary (default for Telegram)
```
**[Topic]**

[2-3 sentence overview]

Key points:
- Point 1
- Point 2
- Point 3

Source: [URL]
```

### Comparative Table
Use when comparing options, tools, or approaches:
```
| Feature | Option A | Option B |
|---------|----------|----------|
| ...     | ...      | ...      |
```

### Deep Report
For complex topics:
1. Executive summary (2-3 sentences)
2. Background / context
3. Current state
4. Key findings (with sources)
5. Open questions / controversies
6. Conclusion

### Citation Format
Always include sources:
- Inline: "According to [Source](URL), ..."
- Reference list at the end for multiple sources

## Research Patterns

### Fact-checking a claim
1. Search for the original source of the claim
2. Find independent verification
3. Check fact-checking sites (Snopes, PolitiFact, etc.)
4. Report confidence level: confirmed / unverified / false

### Comparing technologies/tools
1. Search each option's official documentation
2. Search "[option A] vs [option B]" for community comparisons
3. Look for recent benchmarks or user reviews
4. Summarize pros/cons in a table

### Finding the latest news
1. Search with current year appended
2. Use news-specific search
3. Check official channels / blogs
4. Note publication dates prominently

### Academic/technical research
1. Start with arXiv (see arxiv skill) or Google Scholar
2. Find the seminal/most-cited papers
3. Check recent papers that cite them
4. Look for survey/review papers for overviews

## Quality Checklist

Before delivering results:
- [ ] At least 2-3 independent sources consulted
- [ ] Sources are credible and current
- [ ] Conflicting information noted
- [ ] Key claims have source links
- [ ] Output format matches user's need
- [ ] Telegram-friendly length (no walls of text)

## Notes

- For Telegram: keep responses concise, use bullet points
- Long research results: offer to continue in follow-up messages
- Always cite sources — users should be able to verify
- If information is unavailable or unclear, say so explicitly
