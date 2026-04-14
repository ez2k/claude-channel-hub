---
name: ocr-and-documents
description: Extract text from PDFs and scanned documents. Use web_fetch for remote URLs, pymupdf for local text-based PDFs, marker-pdf for OCR/scanned docs. For DOCX use python-docx.
version: 2.3.0
author: Claude Channel Hub
license: MIT
metadata:
  tags: [PDF, Documents, Research, Text-Extraction, OCR]
  related_skills: [arxiv]
---

# PDF & Document Extraction

For DOCX: use `python-docx` (parses actual document structure, far better than OCR).
This skill covers **PDFs and scanned documents**.

## Step 1: Remote URL Available?

If the document has a URL, **always try `web_fetch` first**:

```
web_fetch("https://arxiv.org/pdf/2402.03300")
web_fetch("https://example.com/report.pdf")
```

This handles PDF-to-text conversion with no local dependencies.

Only use local extraction when: the file is local, web_fetch fails, or you need batch processing.

## Step 2: Choose Local Extractor

| Feature | pymupdf (~25MB) | marker-pdf (~3-5GB) |
|---------|-----------------|---------------------|
| **Text-based PDF** | Yes | Yes |
| **Scanned PDF (OCR)** | No | Yes (90+ languages) |
| **Tables** | Yes (basic) | Yes (high accuracy) |
| **Equations / LaTeX** | No | Yes |
| **Code blocks** | No | Yes |
| **Markdown output** | Yes (via pymupdf4llm) | Yes (native) |
| **Install size** | ~25MB | ~3-5GB (PyTorch + models) |
| **Speed** | Instant | ~1-14s/page (CPU) |

**Decision**: Use pymupdf unless you need OCR, equations, or complex layout analysis.

---

## pymupdf (lightweight)

```bash
pip install pymupdf pymupdf4llm
```

**Extract text:**
```bash
python3 -c "
import pymupdf
doc = pymupdf.open('document.pdf')
for page in doc:
    print(page.get_text())
"
```

**Extract as markdown:**
```bash
python3 -c "
import pymupdf4llm
md_text = pymupdf4llm.to_markdown('document.pdf')
print(md_text)
"
```

**Extract specific pages:**
```bash
python3 -c "
import pymupdf
doc = pymupdf.open('document.pdf')
for i in range(min(5, len(doc))):
    print(f'--- Page {i+1} ---')
    print(doc[i].get_text())
"
```

---

## marker-pdf (high-quality OCR)

```bash
pip install marker-pdf
```

**CLI usage:**
```bash
marker_single document.pdf --output_dir ./output
marker /path/to/folder --workers 4    # Batch
```

**Check disk space first** (~5GB needed for PyTorch + models):
```bash
df -h ~
```

---

## Split, Merge & Search

pymupdf handles these natively:

```python
# Split: extract pages 1-5 to a new PDF
import pymupdf
doc = pymupdf.open("report.pdf")
new = pymupdf.open()
for i in range(5):
    new.insert_pdf(doc, from_page=i, to_page=i)
new.save("pages_1-5.pdf")
```

```python
# Merge multiple PDFs
import pymupdf
result = pymupdf.open()
for path in ["a.pdf", "b.pdf", "c.pdf"]:
    result.insert_pdf(pymupdf.open(path))
result.save("merged.pdf")
```

```python
# Search for text across all pages
import pymupdf
doc = pymupdf.open("report.pdf")
for i, page in enumerate(doc):
    results = page.search_for("revenue")
    if results:
        print(f"Page {i+1}: {len(results)} match(es)")
        print(page.get_text("text"))
```

---

## DOCX Files

```bash
pip install python-docx
```

```python
from docx import Document
doc = Document('document.docx')
for para in doc.paragraphs:
    print(para.text)
```

---

## Notes

- `web_fetch` is always first choice for URLs
- pymupdf is the safe default — instant, no models, works everywhere
- marker-pdf is for OCR, scanned docs, equations, complex layouts — install only when needed
- marker-pdf downloads ~2.5GB of models to `~/.cache/huggingface/` on first use
- For Word docs: `pip install python-docx` (better than OCR — parses actual structure)
