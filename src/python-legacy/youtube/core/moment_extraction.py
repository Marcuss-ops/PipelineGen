import logging
import math
import re
from collections import Counter
from typing import Dict, List

logger = logging.getLogger(__name__)

PRIORITY_KEYWORDS = [
    "important", "significant", "critical", "key", "main", "核心",
    "first", "second", "third", "finally", "lastly",
    "however", "but", "actually", "really", "truth", "fact",
    "believe", "think", "know", "understand",
    "love", "hate", "best", "worst", "amazing", "incredible",
    "money", "business", "company", "product", "launch", "release",
    "fail", "success", "million", "billion", "worth",
    "interview", "talk", "discuss", "explain", "share",
]

HOOK_KEYWORDS = [
    "secret", "truth", "reveal", "shock", "surprise",
    "never", "always", "everyone", "nobody",
    "best", "worst", "amazing", "incredible", "unbelievable",
    "warning", "must", "need", "should", "have to",
]


def parse_timestamp(ts: str) -> float:
    parts = ts.split(":")
    hours = int(parts[0])
    minutes = int(parts[1])
    seconds = float(parts[2])
    return hours * 3600 + minutes * 60 + seconds


def tokenize(text: str) -> List[str]:
    text = text.lower()
    text = re.sub(r"[^\w\s]", " ", text)
    return text.split()


def parse_vtt_to_segments(vtt_content: str) -> List[Dict]:
    segments: List[Dict] = []
    lines = vtt_content.split("\n")
    i = 0
    while i < len(lines):
        line = lines[i].strip()
        if not line or line.startswith("WEBVTT") or line.startswith("Kind:") or line.startswith("Language:"):
            i += 1
            continue
        if "-->" in line:
            match = re.match(r"(\d{2}:\d{2}:\d{2}\.\d{3})\s*-->\s*(\d{2}:\d{2}:\d{2}\.\d{3})", line)
            if match:
                start_time = match.group(1)
                end_time = match.group(2)
                text_lines = []
                i += 1
                while i < len(lines) and lines[i].strip():
                    text_lines.append(lines[i].strip())
                    i += 1
                text = re.sub(r"<[^>]+>", "", " ".join(text_lines)).strip()
                if text:
                    segments.append({
                        "start": start_time,
                        "end": end_time,
                        "text": text,
                        "duration": parse_timestamp(end_time) - parse_timestamp(start_time),
                    })
                continue
        i += 1
    return segments


def calculate_tf_idf_scores(segments: List[Dict], min_word_length: int = 3) -> List[Dict]:
    if not segments:
        return segments
    all_words = []
    segment_words = []
    for seg in segments:
        words = [w for w in tokenize(seg["text"]) if len(w) >= min_word_length]
        segment_words.append(words)
        all_words.extend(words)
    doc_freq = Counter()
    for words in segment_words:
        for word in set(words):
            doc_freq[word] += 1
    num_docs = len(segments)
    idf = {word: math.log(num_docs / (1 + df)) for word, df in doc_freq.items()}
    for idx, seg in enumerate(segments):
        words = segment_words[idx]
        word_count = Counter(words)
        tf = {w: count / len(words) for w, count in word_count.items()} if words else {}
        seg["tfidf_score"] = sum(tf.get(w, 0) * idf.get(w, 0) for w in set(words))
    return segments


def score_segment(seg: Dict, topic_keywords: List[str] = None) -> float:
    score = seg.get("tfidf_score", 0) * 10
    text_lower = seg["text"].lower()
    words = tokenize(seg["text"])
    for keyword in PRIORITY_KEYWORDS:
        if keyword in text_lower:
            score += 0.5
    for keyword in HOOK_KEYWORDS:
        if keyword in text_lower:
            score += 0.3
    if topic_keywords:
        for keyword in topic_keywords:
            if keyword.lower() in text_lower:
                score += 1.0
    word_count = len(words)
    if 10 <= word_count <= 30:
        score += 0.5
    elif 30 < word_count <= 50:
        score += 0.3
    duration = seg.get("duration", 0)
    if 5 <= duration <= 20:
        score += 0.3
    capitalized = len(re.findall(r"\b[A-Z][a-z]+\b", seg["text"]))
    score += min(capitalized * 0.1, 0.5)
    if re.search(r"\d+", seg["text"]):
        score += 0.2
    return score


def extract_key_moments(
    vtt_content: str,
    topic: str = "",
    max_moments: int = 10,
    min_duration: float = 3.0,
    max_duration: float = 60.0,
) -> List[Dict]:
    segments = parse_vtt_to_segments(vtt_content)
    if not segments:
        logger.warning("Nessun segmento trovato nel VTT")
        return []
    segments = [s for s in segments if min_duration <= s.get("duration", 0) <= max_duration]
    segments = calculate_tf_idf_scores(segments)
    topic_keywords = [w.strip() for w in topic.split() if len(w.strip()) > 2] if topic else []
    for seg in segments:
        seg["score"] = score_segment(seg, topic_keywords)
    segments.sort(key=lambda item: item["score"], reverse=True)
    top_moments = segments[:max_moments]
    top_moments.sort(key=lambda item: parse_timestamp(item["start"]))
    return [
        {
            "rank": idx,
            "start": moment["start"],
            "end": moment["end"],
            "text": moment["text"],
            "duration": round(moment.get("duration", 0), 1),
            "score": round(moment["score"], 2),
        }
        for idx, moment in enumerate(top_moments, 1)
    ]


def extract_moments_with_llm(vtt_content: str, topic: str, max_moments: int = 5) -> List[Dict]:
    return extract_key_moments(vtt_content, topic, max_moments)
