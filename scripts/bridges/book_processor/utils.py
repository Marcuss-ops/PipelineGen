import re
from html.parser import HTMLParser

class HTMLTagStripper(HTMLParser):
    def __init__(self):
        super().__init__()
        self.reset()
        self.strict = False
        self.convert_charrefs = True
        self.text = []

    def handle_data(self, d):
        self.text.append(d)

    def get_data(self):
        return ''.join(self.text)

def clean_html(html_content):
    try:
        stripper = HTMLTagStripper()
        stripper.feed(html_content)
        return stripper.get_data().strip()
    except Exception:
        return re.sub(r'<[^>]+>', '', html_content).strip()

def chunk_text(text, max_chars=12000):
    paragraphs = text.split("\n")
    chunks = []
    current_chunk = []
    current_len = 0

    for para in paragraphs:
        para_stripped = para.strip()
        if not para_stripped:
            continue

        if len(para_stripped) > max_chars:
            sentences = re.split(r'(?<=[.!?])\s+', para_stripped)
            for sentence in sentences:
                if current_len + len(sentence) + 1 > max_chars:
                    if current_chunk:
                        chunks.append("\n".join(current_chunk))
                    current_chunk = [sentence]
                    current_len = len(sentence)
                else:
                    current_chunk.append(sentence)
                    current_len += len(sentence) + 1
        else:
            if current_len + len(para_stripped) + 1 > max_chars:
                if current_chunk:
                    chunks.append("\n".join(current_chunk))
                current_chunk = [para_stripped]
                current_len = len(para_stripped)
            else:
                current_chunk.append(para_stripped)
                current_len += len(para_stripped) + 1

    if current_chunk:
        chunks.append("\n".join(current_chunk))

    return chunks

def deduplicate_repetitions(text):
    lines = text.split('\n')
    seen = {}
    result = []
    for line in lines:
        stripped = line.strip()
        if stripped:
            seen[stripped] = seen.get(stripped, 0) + 1
            if seen[stripped] > 3:
                return '\n'.join(result)
        result.append(line)
    return '\n'.join(result)

def clean_output(text):
    text = re.sub(r'\([^)]*\)', '', text)
    text = re.sub(r'\[[^\]]*\]', '', text)
    text = re.sub(r'\*\*[^*]*\*\*', '', text)
    text = re.sub(r'(?i)(audiobook\s+chapter\s+(begins?|ends?|starts?)),?', '', text)
    text = re.sub(r'(?i)(chapter\s+(start|end|begins?)),?', '', text)
    text = re.sub(r'#+\s*', '', text)
    text = re.sub(r'\n{3,}', '\n\n', text)
    return text.strip()
