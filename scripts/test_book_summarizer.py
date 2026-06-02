#!/usr/bin/env python3
"""
Unit tests for book_summarizer.py functions.
Run with: python3 scripts/test_book_summarizer.py
"""

import sys
import unittest
import re
import json
from unittest import mock
from pathlib import Path

# Add scripts directory to path for imports
sys.path.insert(0, str(Path(__file__).parent))


class TestCleanHtml(unittest.TestCase):
    """Tests for clean_html function."""
    
    def test_simple_html_tags_removed(self):
        """Test that basic HTML tags are stripped."""
        from book_summarizer import clean_html
        result = clean_html("<p>Hello <b>world</b></p>")
        self.assertEqual(result, "Hello world")
    
    def test_nested_html_tags_removed(self):
        """Test that nested HTML tags are stripped."""
        from book_summarizer import clean_html
        result = clean_html("<div><p><span>Nested</span> text</p></div>")
        self.assertEqual(result, "Nested text")
    
    def test_html_with_attributes_removed(self):
        """Test that HTML tags with attributes are stripped."""
        from book_summarizer import clean_html
        result = clean_html('<p class="test" id="main">Content</p>')
        self.assertEqual(result, "Content")
    
    def test_plain_text_unchanged(self):
        """Test that plain text remains unchanged."""
        from book_summarizer import clean_html
        result = clean_html("Just plain text here")
        self.assertEqual(result, "Just plain text here")
    
    def test_whitespace_handling(self):
        """Test that whitespace is preserved correctly."""
        from book_summarizer import clean_html
        result = clean_html("<p>Hello   World</p>")
        self.assertEqual(result, "Hello   World")
    
    def test_empty_string(self):
        """Test that empty string returns empty string."""
        from book_summarizer import clean_html
        result = clean_html("")
        self.assertEqual(result, "")
    
    def test_self_closing_tags(self):
        """Test that self-closing tags like <br/> are stripped."""
        from book_summarizer import clean_html
        result = clean_html("Line 1<br/>Line 2")
        self.assertEqual(result, "Line 1Line 2")


class TestChunkText(unittest.TestCase):
    """Tests for chunk_text function."""
    
    def test_short_text_single_chunk(self):
        """Test that short text returns single chunk."""
        from book_summarizer import chunk_text
        text = "This is a short piece of text."
        result = chunk_text(text, max_chars=100)
        self.assertEqual(len(result), 1)
        self.assertIn("short piece", result[0])
    
    def test_long_text_multiple_chunks(self):
        """Test that long text is split into multiple chunks."""
        from book_summarizer import chunk_text
        text = "Sentence one. " * 1000
        result = chunk_text(text, max_chars=2000)
        self.assertGreater(len(result), 1)
    
    def test_paragraph_preservation(self):
        """Test that paragraphs are preserved in chunks."""
        from book_summarizer import chunk_text
        text = "First paragraph.\n\nSecond paragraph."
        result = chunk_text(text, max_chars=100)
        combined = "\n".join(result)
        self.assertIn("First paragraph", combined)
        self.assertIn("Second paragraph", combined)
    
    def test_empty_text_returns_empty_list(self):
        """Test that empty text returns empty list."""
        from book_summarizer import chunk_text
        result = chunk_text("", max_chars=100)
        self.assertEqual(result, [])
    
    def test_single_char_max_chars(self):
        """Test behavior with max_chars=1."""
        from book_summarizer import chunk_text
        text = "Short"
        result = chunk_text(text, max_chars=1)
        self.assertGreater(len(result), 0)


class TestDeduplicateRepetitions(unittest.TestCase):
    """Tests for deduplicate_repetitions function."""
    
    def test_no_duplicates_unchanged(self):
        """Test that text without repetition is unchanged."""
        from book_summarizer import deduplicate_repetitions
        text = "This is a unique sentence.\nAnother different one."
        result = deduplicate_repetitions(text)
        self.assertEqual(result, text)
    
    def test_repeated_line_truncated(self):
        """Test that same line appearing >3 times is truncated."""
        from book_summarizer import deduplicate_repetitions
        text = "Line one.\nSame line.\nSame line.\nSame line.\nSame line.\nAfter repetition."
        result = deduplicate_repetitions(text)
        lines = result.split("\n")
        same_count = sum(1 for l in lines if l.strip() == "Same line.")
        self.assertLessEqual(same_count, 3)
    
    def test_different_lines_not_affected(self):
        """Test that different repeated lines are not affected."""
        from book_summarizer import deduplicate_repetitions
        text = "First.\nSecond.\nThird.\nFirst.\nSecond.\nThird."
        result = deduplicate_repetitions(text)
        self.assertIn("First", result)
        self.assertIn("Second", result)
        self.assertIn("Third", result)


class TestTranslateTextOllama(unittest.TestCase):
    """Tests for translate_text_ollama function with mocked Ollama."""
    
    @mock.patch('book_summarizer.urllib.request.urlopen')
    def test_translation_success(self, mock_urlopen):
        """Test successful translation."""
        from book_summarizer import translate_text_ollama
        
        mock_response = json.dumps({"response": "Hola mundo"}).encode()
        mock_urlopen.return_value.__enter__.return_value.read.return_value = mock_response
        
        result = translate_text_ollama("Hello world", "es", model="gemma3:12b")
        self.assertEqual(result, "Hola mundo")
    
    @mock.patch('book_summarizer.urllib.request.urlopen')
    def test_translation_fallback_on_error(self, mock_urlopen):
        """Test fallback when first model fails."""
        from book_summarizer import translate_text_ollama
        
        def side_effect(*args, **kwargs):
            raise Exception("Model failed")
        
        mock_urlopen.side_effect = side_effect
        
        result = translate_text_ollama("Hello world", "es", model="gemma3:12b")
        self.assertEqual(result, "")


class TestExtractGoogleDocId(unittest.TestCase):
    """Tests for Google Doc ID extraction logic (URL parsing validation)."""
    
    def test_drive_url_with_edit_suffix(self):
        """Test extracting ID from URL with /edit suffix."""
        url = "https://docs.google.com/document/d/18KDv7lrdU9_ueZ7NEFuzezjtK_9F55RUnrywe__dxZg/edit"
        match = re.search(r'/d/([^/]+)', url)
        doc_id = match.group(1) if match else ""
        self.assertEqual(doc_id, "18KDv7lrdU9_ueZ7NEFuzezjtK_9F55RUnrywe__dxZg")
    
    def test_drive_url_with_query_params(self):
        """Test extracting ID from URL with query parameters."""
        url = "https://docs.google.com/document/d/ABC123/edit?tab=t.0"
        match = re.search(r'/d/([^/]+)', url)
        doc_id = match.group(1) if match else ""
        self.assertEqual(doc_id, "ABC123")
    
    def test_drive_url_without_suffix(self):
        """Test extracting ID from URL without /edit suffix."""
        url = "https://docs.google.com/document/d/DEF456"
        match = re.search(r'/d/([^/]+)', url)
        doc_id = match.group(1) if match else ""
        self.assertEqual(doc_id, "DEF456")
    
    def test_invalid_url_returns_empty(self):
        """Test that invalid URL returns empty string."""
        url = "https://not-google-docs.com/doc/123"
        match = re.search(r'/d/([^/]+)', url)
        doc_id = match.group(1) if match else ""
        self.assertEqual(doc_id, "")


class TestSanitization(unittest.TestCase):
    """Tests for title sanitization logic used in folder naming.
    
    The actual logic in book_summarizer.py is:
    safe_name = re.sub(r'[^\w\s-]', '', google_doc_title).strip()[:50]
    """
    
    def test_special_chars_removed(self):
        """Test that special characters are removed from titles."""
        title = "Test: Title (2023) | Part 1"
        safe_name = re.sub(r'[^\w\s-]', '', title).strip()[:50]
        self.assertIn("Test", safe_name)
        self.assertNotIn(":", safe_name)
        self.assertNotIn("|", safe_name)
    
    def test_title_truncation(self):
        """Test that long titles are truncated."""
        long_title = "A" * 100
        safe_name = re.sub(r'[^\w\s-]', '', long_title).strip()[:50]
        self.assertEqual(len(safe_name), 50)
    
    def test_empty_title_fallback(self):
        """Test fallback when title is empty."""
        empty_title = ""
        safe_name = re.sub(r'[^\w\s-]', '', empty_title).strip()[:50] if empty_title else ""
        self.assertEqual(safe_name, "")
    
    def test_preserves_hyphens_and_spaces(self):
        """Test that hyphens and spaces are preserved."""
        title = "How To Live Frugal - A Guide"
        safe_name = re.sub(r'[^\w\s-]', '', title).strip()[:50]
        self.assertIn("How To Live Frugal - A Guide", safe_name)


class TestLanguageCodes(unittest.TestCase):
    """Tests for language code validation."""
    
    def test_supported_languages_exist(self):
        """Test that all expected language codes are defined."""
        from book_summarizer import LANGUAGES
        expected = ["en", "es", "fr", "de", "it", "pt", "pl", "nl", "ja", "ko", "ru", "tr", "id", "zh", "ar", "hi"]
        for lang in expected:
            self.assertIn(lang, LANGUAGES, f"Language {lang} should be in LANGUAGES dict")
    
    def test_language_names_are_strings(self):
        """Test that all language names are non-empty strings."""
        from book_summarizer import LANGUAGES
        for code, name in LANGUAGES.items():
            self.assertIsInstance(code, str)
            self.assertIsInstance(name, str)
            self.assertGreater(len(name), 0)


class TestCleanOutput(unittest.TestCase):
    """Tests for clean_output function."""
    
    def test_stage_directions_removed(self):
        """Test that stage directions are removed."""
        from book_summarizer import clean_output
        text = "(Audiobook Chapter Begins) Some content (Chapter End)"
        result = clean_output(text)
        self.assertNotIn("Audiobook Chapter Begins", result)
        self.assertNotIn("Chapter End", result)
    
    def test_square_brackets_removed(self):
        """Test that square bracket content is removed."""
        from book_summarizer import clean_output
        text = "Before [some note] after"
        result = clean_output(text)
        self.assertNotIn("[some note]", result)
    
    def test_bold_markdown_removed(self):
        """Test that bold markdown is removed."""
        from book_summarizer import clean_output
        text = "Some **bold** text"
        result = clean_output(text)
        self.assertNotIn("**bold**", result)
    
    def test_multiple_newlines_reduced(self):
        """Test that multiple newlines are reduced."""
        from book_summarizer import clean_output
        text = "First\n\n\n\n\nSecond"
        result = clean_output(text)
        self.assertNotIn("\n\n\n", result)


if __name__ == "__main__":
    unittest.main(verbosity=2)