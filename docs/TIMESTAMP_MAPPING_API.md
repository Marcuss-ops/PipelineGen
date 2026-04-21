# Timestamp-to-Clip Mapping API

## Overview

The **Timestamp Mapping** system automatically links text segments (with timestamps) to existing clips on **Google Drive** and **Artlist**. 

### Use Case

When your script has segments like:
```
[0:00-0:15] "Elon Musk presenta il nuovo robot AI"
[0:15-0:30] "La tecnologia rivoluziona il settore"
[0:30-0:45] "Intervista esclusiva con il CEO"
```

The system finds the **best matching clips** from your existing Drive/Artlist library for each segment.

---

## API Endpoint

### Map Text Segments to Clips

**Endpoint**: `POST /api/timestamp/map`

Maps text segments with timestamps to relevant clips from Drive and/or Artlist.

**Request Body**:
```json
{
  "script_id": "script_123456",
  "segments": [
    {
      "id": "seg_1",
      "index": 0,
      "start_time": 0.0,
      "end_time": 15.0,
      "text": "Elon Musk presenta il nuovo robot AI",
      "keywords": ["robot", "AI", "tecnologia"],
      "entities": ["Elon Musk"],
      "emotions": ["excitement"]
    },
    {
      "id": "seg_2",
      "index": 1,
      "start_time": 15.0,
      "end_time": 30.0,
      "text": "La tecnologia rivoluziona il settore automotive",
      "keywords": ["tecnologia", "automotive", "innovazione"],
      "entities": [],
      "emotions": ["innovation"]
    }
  ],
  "media_type": "clip",
  "max_clips_per_segment": 3,
  "min_score": 20,
  "include_drive": true,
  "include_artlist": true
}
```

**Fields**:

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `script_id` | string | ✅ Yes | ID dello script |
| `segments` | array | ✅ Yes | Segmenti di testo con timestamp |
| `segments[].id` | string | ❌ No | ID univoco del segmento |
| `segments[].index` | int | ❌ No | Posizione nel testo (0-based) |
| `segments[].start_time` | float | ❌ No | Start time in secondi |
| `segments[].end_time` | float | ❌ No | End time in secondi |
| `segments[].text` | string | ❌ No | Testo del segmento |
| `segments[].keywords` | string[] | ❌ No | Keywords estratte |
| `segments[].entities` | string[] | ❌ No | Entità nominate |
| `segments[].emotions` | string[] | ❌ No | Emozioni del segmento |
| `media_type` | string | ❌ No | "clip" o "stock" (default: any) |
| `max_clips_per_segment` | int | ❌ No | Max clip per segmento (default: 3) |
| `min_score` | float | ❌ No | Score minimo 0-100 (default: 20) |
| `include_drive` | bool | ❌ No | Cerca su Drive (default: true) |
| `include_artlist` | bool | ❌ No | Cerca su Artlist (default: true) |

---

**Response** (Success):
```json
{
  "success": true,
  "mapping": {
    "script_id": "script_123456",
    "total_duration": 30.0,
    "average_score": 75.5,
    "created_at": "2026-04-11T16:30:00Z",
    "segments": [
      {
        "segment": {
          "id": "seg_1",
          "index": 0,
          "start_time": 0.0,
          "end_time": 15.0,
          "text": "Elon Musk presenta il nuovo robot AI",
          "keywords": ["robot", "AI", "tecnologia"],
          "entities": ["Elon Musk"],
          "emotions": ["excitement"]
        },
        "assigned_clips": [
          {
            "clip_id": "drive_file_abc123",
            "source": "drive",
            "name": "Elon Musk Robot Demo",
            "folder_path": "Tech & AI/Robotics",
            "relevance_score": 95.0,
            "duration": 45.0,
            "drive_link": "https://drive.google.com/file/d/abc123",
            "match_reason": "Entity 'Elon Musk' matches tag 'elon musk'"
          },
          {
            "clip_id": "artlist_xyz789",
            "source": "artlist",
            "name": "AI Technology Presentation",
            "folder_path": "Artlist/Tech & AI",
            "relevance_score": 80.0,
            "duration": 30.0,
            "drive_link": "https://artlist.io/video/xyz789",
            "match_reason": "Artlist match: AI Technology Presentation"
          }
        ],
        "best_score": 95.0,
        "clip_count": 2
      },
      {
        "segment": {
          "id": "seg_2",
          "index": 1,
          "start_time": 15.0,
          "end_time": 30.0,
          "text": "La tecnologia rivoluziona il settore automotive",
          "keywords": ["tecnologia", "automotive", "innovazione"],
          "entities": [],
          "emotions": ["innovation"]
        },
        "assigned_clips": [
          {
            "clip_id": "drive_file_def456",
            "source": "drive",
            "name": "Automotive Technology Innovation",
            "folder_path": "Tech & AI/Automotive",
            "relevance_score": 56.0,
            "duration": 40.0,
            "drive_link": "https://drive.google.com/file/d/def456",
            "match_reason": "Keyword 'tecnologia' matches tag 'technology'"
          }
        ],
        "best_score": 56.0,
        "clip_count": 1
      }
    ]
  },
  "total_segments": 2,
  "total_clips": 3,
  "average_score": 75.5
}
```

---

**Response** (Error):
```json
{
  "success": false,
  "error": "Invalid request: key: 'segments' Error:Field validation for 'segments' failed on the 'required' tag"
}
```

---

## How It Works

### 1. Segment Analysis

For each segment, the system extracts:
- **Keywords** (from text)
- **Entities** (named entities like people, places)
- **Emotions** (mood/context)

### 2. Clip Search

Builds search queries from segment data:
```
Query = keywords + entities + emotions
Example: "Elon Musk robot AI tecnologia excitement"
```

### 3. Semantic Matching

Uses the **SemanticSuggester** to score clips:
- **Entity match**: 100 points (highest)
- **Entity in name**: 80 points
- **Entity in folder**: 60 points
- **Keyword in tags**: 25 points per match
- **Keyword in name**: 20 points
- **Keyword in folder**: 15 points
- **Action verb**: 30 points
- **Exact phrase**: 50 points

### 4. Results Ranking

For each segment:
1. Collects clips from Drive + Artlist
2. Sorts by relevance score (descending)
3. Limits to `max_clips_per_segment`
4. Returns top matches

---

## Examples

### Example 1: Basic Mapping

```bash
curl -X POST http://localhost:8080/api/timestamp/map \
  -H "Content-Type: application/json" \
  -d '{
    "script_id": "my_script",
    "segments": [
      {
        "start_time": 0,
        "end_time": 10,
        "text": "Intelligenza artificiale e robot",
        "keywords": ["AI", "robot"],
        "entities": []
      }
    ],
    "max_clips_per_segment": 5,
    "min_score": 30
  }'
```

### Example 2: Only Drive Clips

```bash
curl -X POST http://localhost:8080/api/timestamp/map \
  -H "Content-Type: application/json" \
  -d '{
    "script_id": "my_script",
    "segments": [
      {
        "start_time": 0,
        "end_time": 15,
        "text": "Elon Musk presenta Tesla Bot",
        "keywords": ["Tesla", "robot"],
        "entities": ["Elon Musk"]
      }
    ],
    "include_drive": true,
    "include_artlist": false
  }'
```

### Example 3: Only Artlist, Stock Media

```bash
curl -X POST http://localhost:8080/api/timestamp/map \
  -H "Content-Type: application/json" \
  -d '{
    "script_id": "my_script",
    "segments": [
      {
        "start_time": 0,
        "end_time": 20,
        "text": "Paesaggio naturale con montagne",
        "keywords": ["natura", "montagna", "paesaggio"],
        "entities": []
      }
    ],
    "media_type": "stock",
    "include_drive": false,
    "include_artlist": true
  }'
```

### Example 4: High-Quality Matches Only

```bash
curl -X POST http://localhost:8080/api/timestamp/map \
  -H "Content-Type: application/json" \
  -d '{
    "script_id": "my_script",
    "segments": [...],
    "min_score": 70,
    "max_clips_per_segment": 2
  }'
```

---

## Integration with Script Parser

You can use the output from the **Script Parser** directly:

```go
// 1. Parse script into scenes
parser := script.NewParser(60, "it")
script, _ := parser.Parse(text, "My Script", "informative", "ollama")

// 2. Convert scenes to timestamp segments
segments := make([]timestamp.TextSegment, len(script.Scenes))
currentTime := 0.0
for i, scene := range script.Scenes {
    segments[i] = timestamp.TextSegment{
        Index:     i,
        StartTime: currentTime,
        EndTime:   currentTime + float64(scene.Duration),
        Text:      scene.Text,
        Keywords:  scene.Keywords,
        Entities:  scene.EntitiesText(),
        Emotions:  scene.Emotions,
    }
    currentTime += float64(scene.Duration)
}

// 3. Map to clips
mappingReq := &timestamp.MappingRequest{
    ScriptID:           script.ID,
    Segments:           segments,
    MaxClipsPerSegment: 3,
    MinScore:           20,
    IncludeDrive:       true,
    IncludeArtlist:     true,
}

mapping, _ := timestampService.MapSegmentsToClips(ctx, mappingReq)

// 4. Use the mapping
for _, seg := range mapping.Segments {
    fmt.Printf("Segment %d (%.1f-%.1f): %d clips found\n", 
        seg.Segment.Index, 
        seg.Segment.StartTime, 
        seg.Segment.EndTime,
        seg.ClipCount)
    
    for _, clip := range seg.AssignedClips {
        fmt.Printf("  - %s (score: %.0f, source: %s)\n", 
            clip.Name, clip.RelevanceScore, clip.Source)
    }
}
```

---

## File Structure

```
internal/
├── timestamp/
│   ├── mapper.go          # Types and interfaces
│   └── service.go         # Mapping logic implementation
├── api/handlers/
│   └── timestamp.go       # HTTP handler
└── cmd/server/
    └── main.go            # Service initialization
```

---

## Testing

Run tests:
```bash
cd /home/pierone/Pyt/VeloxEditing/refactored/src/go-master
go test ./internal/timestamp -v
```

---

## Scoring Details

### Entity Matching (Highest Priority)

| Match Type | Points | Example |
|------------|--------|---------|
| Entity in clip tags | 100 | "Elon Musk" in tags |
| Entity in clip name | 80 | "Elon Musk" in name |
| Entity in folder path | 60 | "Elon Musk" in folder |

### Keyword Matching (Medium Priority)

| Match Type | Points | Example |
|------------|--------|---------|
| Keyword in tags | 25 | "robot" in tags |
| Keyword in name | 20 | "robot" in name |
| Keyword in folder | 15 | "robot" in folder |

### Bonus Points

| Match Type | Points | Example |
|------------|--------|---------|
| Action verb | 30 | "presenta" → "presentation" |
| Exact phrase | 50 | Full sentence match |
| Group match | 15 | "tech" → "Tech & AI" group |

**Max score**: 100 (normalized)

---

## Requirements

- **Google Drive API**: Configured with credentials
- **Artlist Database**: SQLite database with clip metadata
- **Clip Indexer**: Must have scanned Drive clips

---

## Future Enhancements

- [ ] Auto-generate segments from Whisper transcription
- [ ] Support for YouTube clip suggestions
- [ ] Clip duration matching (match clip length to segment duration)
- [ ] Multi-language support (auto-translate keywords)
- [ ] Visual preview of mapped clips
- [ ] Export mapping as JSON for video editing software
