import html
from pathlib import Path

try:
    from reportlab.lib.pagesizes import A4
    from reportlab.lib.styles import ParagraphStyle, getSampleStyleSheet
    from reportlab.platypus import SimpleDocTemplate, Paragraph, Spacer, PageBreak, Table, TableStyle
    from reportlab.lib.enums import TA_LEFT, TA_CENTER, TA_JUSTIFY
    from reportlab.lib.colors import HexColor
    from reportlab.pdfgen import canvas
    HAS_REPORTLAB = True
except ImportError:
    HAS_REPORTLAB = False

# Define beautiful theme presets
THEMES = {
    "default": {
        "primary_color": HexColor("#111827"),     # Dark charcoal
        "secondary_color": HexColor("#4B5563"),   # Cool gray
        "body_color": HexColor("#111827"),
        "bg_color": HexColor("#FFFFFF"),
        "font_title": "Helvetica-Bold",
        "font_heading": "Helvetica-Bold",
        "font_body": "Helvetica",
        "title_size": 28,
        "heading_size": 14,
        "body_size": 11,
        "leading_ratio": 1.4,
        "alignment": TA_LEFT,
        "show_header_line": False,
        "cover_layout": "simple"
    },
    "modern": {
        "primary_color": HexColor("#1E3A8A"),     # Deep navy blue
        "secondary_color": HexColor("#2563EB"),   # Royal blue accent
        "body_color": HexColor("#1F2937"),        # Soft charcoal
        "bg_color": HexColor("#F9FAFB"),          # Light gray sheet
        "font_title": "Helvetica-Bold",
        "font_heading": "Helvetica-Bold",
        "font_body": "Helvetica",
        "title_size": 32,
        "heading_size": 15,
        "body_size": 10.5,
        "leading_ratio": 1.5,
        "alignment": TA_JUSTIFY,
        "show_header_line": True,
        "cover_layout": "clean"
    },
    "classic": {
        "primary_color": HexColor("#4A0E17"),     # Deep burgundy
        "secondary_color": HexColor("#7F1D1D"),   # Rich dark red
        "body_color": HexColor("#1C1917"),        # Warm stone dark
        "bg_color": HexColor("#FFFBEB"),          # Light warm amber cream
        "font_title": "Times-Bold",
        "font_heading": "Times-Bold",
        "font_body": "Times-Roman",
        "title_size": 30,
        "heading_size": 14,
        "body_size": 11,
        "leading_ratio": 1.45,
        "alignment": TA_JUSTIFY,
        "show_header_line": True,
        "cover_layout": "classic"
    },
    "academic": {
        "primary_color": HexColor("#0F172A"),     # Slate 900
        "secondary_color": HexColor("#475569"),   # Slate 600
        "body_color": HexColor("#0F172A"),
        "bg_color": HexColor("#FFFFFF"),
        "font_title": "Times-Bold",
        "font_heading": "Times-Bold",
        "font_body": "Times-Roman",
        "title_size": 24,
        "heading_size": 13,
        "body_size": 10.5,
        "leading_ratio": 1.35,
        "alignment": TA_JUSTIFY,
        "show_header_line": True,
        "cover_layout": "formal"
    },
    "colorful": {
        "primary_color": HexColor("#0F766E"),     # Teal 700
        "secondary_color": HexColor("#06B6D4"),   # Cyan 500
        "body_color": HexColor("#1E293B"),        # Slate 800
        "bg_color": HexColor("#F0FDFA"),          # Light mint/teal wash
        "font_title": "Helvetica-Bold",
        "font_heading": "Helvetica-Bold",
        "font_body": "Helvetica",
        "title_size": 32,
        "heading_size": 16,
        "body_size": 11,
        "leading_ratio": 1.5,
        "alignment": TA_LEFT,
        "show_header_line": True,
        "cover_layout": "creative"
    }
}

class NumberedCanvas(canvas.Canvas):
    """
    Two-pass canvas to dynamically compute and render total pages,
    running headers, page dividers, and footers.
    """
    def __init__(self, *args, title="", theme=None, **kwargs):
        super().__init__(*args, **kwargs)
        self._saved_page_states = []
        self.title = title
        self.theme = theme or THEMES["default"]

    def showPage(self):
        self._saved_page_states.append(dict(self.__dict__))
        self._startPage()

    def save(self):
        num_pages = len(self._saved_page_states)
        for state in self._saved_page_states:
            self.__dict__.update(state)
            self.draw_page_elements(num_pages)
            super().showPage()
        super().save()

    def draw_page_elements(self, page_count):
        # Suppress headers and footers on the cover page
        if self._pageNumber == 1:
            return
            
        self.saveState()
        
        width, height = self._pagesize
        margin = 54  # 0.75 in margin
        
        primary_color = self.theme["primary_color"]
        secondary_color = self.theme["secondary_color"]
        font_body = self.theme["font_body"]
        show_line = self.theme["show_header_line"]
        
        # 1. Header Text
        self.setFont(font_body, 8)
        self.setFillColor(secondary_color)
        clean_title = self.title[:70] + ("..." if len(self.title) > 70 else "")
        self.drawString(margin, height - margin + 15, clean_title.upper())
        
        # 2. Header Line (Divider)
        if show_line:
            self.setStrokeColor(secondary_color)
            self.setLineWidth(0.5)
            self.line(margin, height - margin + 8, width - margin, height - margin + 8)
            
        # 3. Footer Page Numbers
        self.setFont(font_body, 8.5)
        self.setFillColor(secondary_color)
        page_text = f"Page {self._pageNumber} of {page_count}"
        self.drawRightString(width - margin, 36, page_text)
        
        self.restoreState()

def generate_pdf(text_path: Path, output_path: Path, title: str = "", style_name: str = "modern") -> bool:
    if not HAS_REPORTLAB:
        print("  reportlab not installed, skipping PDF generation. Install with: pip install reportlab")
        return False
    
    # Fallback to modern if styling is invalid
    theme = THEMES.get(style_name.lower(), THEMES["modern"])
    
    try:
        with open(text_path, "r", encoding="utf-8") as f:
            content = f.read()
        
        # Standard page configuration
        doc = SimpleDocTemplate(
            str(output_path), 
            pagesize=A4,
            rightMargin=54, 
            leftMargin=54,
            topMargin=54, 
            bottomMargin=54
        )
        
        story = []
        styles = getSampleStyleSheet()
        
        # Base document size dimensions for cover block computations
        width, height = A4
        printable_width = width - 108  # width - margins (54 * 2)
        
        # Define paragraph styles derived from active theme
        title_style = ParagraphStyle(
            'CustomTitle',
            parent=styles['Heading1'],
            fontName=theme["font_title"],
            fontSize=theme["title_size"],
            leading=theme["title_size"] + 6,
            textColor=theme["primary_color"],
            alignment=TA_CENTER if theme["cover_layout"] in ["classic", "formal"] else TA_LEFT,
            spaceAfter=15,
        )
        
        subtitle_style = ParagraphStyle(
            'CustomSubtitle',
            parent=styles['Normal'],
            fontName=theme["font_body"],
            fontSize=12,
            leading=16,
            textColor=theme["secondary_color"],
            alignment=TA_CENTER if theme["cover_layout"] in ["classic", "formal"] else TA_LEFT,
            spaceAfter=25,
        )
        
        meta_style = ParagraphStyle(
            'MetaStyle',
            parent=styles['Normal'],
            fontName=theme["font_body"],
            fontSize=9,
            leading=12,
            textColor=theme["secondary_color"],
            alignment=TA_CENTER if theme["cover_layout"] in ["classic", "formal"] else TA_LEFT,
        )
        
        body_style = ParagraphStyle(
            'CustomBody',
            parent=styles['Normal'],
            fontName=theme["font_body"],
            fontSize=theme["body_size"],
            leading=int(theme["body_size"] * theme["leading_ratio"]),
            textColor=theme["body_color"],
            alignment=theme["alignment"],
            spaceAfter=10,
        )
        
        heading_style = ParagraphStyle(
            'CustomHeading',
            parent=styles['Heading2'],
            fontName=theme["font_heading"],
            fontSize=theme["heading_size"],
            leading=theme["heading_size"] + 4,
            textColor=theme["primary_color"],
            spaceBefore=18,
            spaceAfter=8,
            keepWithNext=True,
        )

        # ------------------ COVER PAGE GENERATION ------------------
        layout = theme["cover_layout"]
        
        if layout == "classic":
            story.append(Spacer(1, 150))
            story.append(Paragraph(html.escape(title), title_style))
            # Elegant thin rule separator
            rule = Table([[""]], colWidths=[120], rowHeights=[1])
            rule.setStyle(TableStyle([
                ('BACKGROUND', (0,0), (-1,-1), theme["primary_color"]),
                ('BOTTOMPADDING', (0,0), (-1,-1), 0),
                ('TOPPADDING', (0,0), (-1,-1), 0),
            ]))
            story.append(Spacer(1, 15))
            story.append(rule)
            story.append(Spacer(1, 15))
            story.append(Paragraph("BOOK SUMMARY & KEY INSIGHTS", subtitle_style))
            story.append(Spacer(1, 180))
            story.append(Paragraph("Generated by Book Summarizer System", meta_style))
            
        elif layout == "formal":
            story.append(Spacer(1, 120))
            # Top solid band
            band = Table([[""]], colWidths=[printable_width], rowHeights=[2])
            band.setStyle(TableStyle([
                ('BACKGROUND', (0,0), (-1,-1), theme["primary_color"]),
            ]))
            story.append(band)
            story.append(Spacer(1, 30))
            story.append(Paragraph(html.escape(title), title_style))
            story.append(Paragraph("A Comprehensive Reading Guide & Insights", subtitle_style))
            story.append(Spacer(1, 30))
            story.append(band)
            story.append(Spacer(1, 180))
            story.append(Paragraph("PipelineGen Intelligence Service", meta_style))
            
        elif layout in ["clean", "creative"]:
            story.append(Spacer(1, 80))
            # Accent bar matching theme primary color
            bar = Table([[""]], colWidths=[60], rowHeights=[8])
            bar.setStyle(TableStyle([
                ('BACKGROUND', (0,0), (-1,-1), theme["primary_color"]),
                ('BOTTOMPADDING', (0,0), (-1,-1), 0),
                ('TOPPADDING', (0,0), (-1,-1), 0),
            ]))
            story.append(bar)
            story.append(Spacer(1, 25))
            story.append(Paragraph(html.escape(title), title_style))
            story.append(Paragraph("EXECUTIVE BOOK SUMMARY & CRITICAL TAKEAWAYS", subtitle_style))
            story.append(Spacer(1, 180))
            
            # Metadata block info
            story.append(Paragraph("<b>Source:</b> Digital Edition Processing Queue<br/>"
                                   "<b>System:</b> PipelineGen AI & Ollama Engine<br/>"
                                   "<b>Style Profile:</b> " + style_name.capitalize(), meta_style))
        else: # Simple/Fallback cover
            story.append(Spacer(1, 100))
            story.append(Paragraph(html.escape(title), title_style))
            story.append(Spacer(1, 20))
            story.append(Paragraph("Book Summary", subtitle_style))
            
        story.append(PageBreak())
        
        # ------------------ BODY CONTENT GENERATION ------------------
        paragraphs = content.split("\n\n")
        for para in paragraphs:
            para = para.strip()
            if not para:
                continue
            
            # Clean up residual/awkward line breaks inside paragraph
            para = " ".join(para.split())
            para_escaped = html.escape(para)
            
            # Determine if this paragraph acts as a section heading
            # Rules: Short, doesn't end with sentence-ending punctuation, and fits constraints
            is_heading = len(para) < 100 and not para[-1] in '.!?:'
            
            if is_heading:
                story.append(Paragraph(para_escaped, heading_style))
            else:
                story.append(Paragraph(para_escaped, body_style))
        
        # Build document with multi-pass canvas
        doc.build(story, canvasmaker=lambda *args, **kwargs: NumberedCanvas(*args, title=title, theme=theme, **kwargs))
        print(f"  Generated PDF (Style: {style_name}): {output_path}")
        return True
        
    except Exception as e:
        print(f"  PDF generation error: {e}")
        import traceback
        traceback.print_exc()
        return False