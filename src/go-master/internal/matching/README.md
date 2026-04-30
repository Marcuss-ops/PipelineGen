# Matching & Scoring System

The `matching` package provides the core logic for ranking and selecting assets (clips, images, music) based on textual descriptions, script phrases, or keywords.

## Core Concepts

### 1. Tokenization & Normalization
Before any comparison, text is processed through:
- **Normalization**: Converting to lowercase, replacing separators (`_`, `-`, `.`) with spaces, and trimming.
- **Tokenization**: Splitting text into individual words using unicode-aware boundaries.
- **Stop Word Removal**: Optional filtering of common words (e.g., "the", "and", "per", "con") that don't add semantic value.

### 2. Scoring Algorithm
The primary scoring function is `ScoreAsset`, which calculates a score from **0 to 100**.

#### Base Score (Token Overlap)
The base score is calculated using token overlap between the search phrase and the asset's metadata (Name, Filename, Folder, and Tags):
```go
base = (matching_tokens / total_phrase_tokens) * 100
```

#### Boosts
To improve accuracy, "boosts" are added to the base score based on where the match occurs:
- **Name Match Boost (+20)**: If the phrase contains the asset's display name.
- **Filename Match Boost (+18)**: If the phrase contains the filename (excluding extension).
- **Folder Match Boost (+10)**: If the match occurs in the folder path.
- **Topic/Side Text Boost (+5)**: For general metadata matches.

The final score is capped at **100**.

## Configuration
The scoring behavior can be tuned using the `ScoringConfig` struct:

```go
type ScoringConfig struct {
    NameMatchBoost     float64
    FilenameMatchBoost float64
    FolderMatchBoost   float64
    TopicMatchBoost    float64
    SideTextBoost      float64
}
```

## Usage Example

```go
matcher := matching.NewMatcher()
score, reason := matcher.ScoreAsset(
    "espresso coffee making", 
    "Coffee Machine", 
    "espresso_01.mp4", 
    "kitchen/machines", 
    "coffee, drink"
)
// result: score=100, reason="token_overlap+boost"
```

## Internal Files
- `matcher.go`: Main entry point and `Matcher` struct.
- `scoring.go`: Implementation of the scoring algorithms.
- `tokenizer.go`: Text processing utilities.
- `types.go`: Common data structures.
