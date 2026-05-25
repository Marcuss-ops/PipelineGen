# PipelineGen: Future Intelligent Implementations

This document outlines proposed evolutions to make PipelineGen a smarter, more autonomous and "creative" media processing system, leveraging the unified database and Hybrid Search capabilities.

---

## 1. Vision-Based Auto-Tagging & Enrichment
**Goal**: Automatically enrich metadata for assets with sparse or missing descriptions.

*   **Logic**: Use the embedding server (CLIP) to analyze the center frame of video clips and images.
*   **Action**: Extract descriptive tags (e.g., "drone shot", "urban sunset", "high contrast") and save them in the `tags` DB field.
*   **Impact**: Drastic improvement in semantic search without manual intervention.

## 2. Narrative Continuity & Diversity Scoring
**Goal**: Improve automatic "editing" quality by avoiding visual repetition and ensuring stylistic consistency.

*   **Diversity Penalty**: If a clip has a `phash` (visual hash) too similar to an already selected clip, its score is penalized to favor variety.
*   **Style Matching**: Analyze the color palette or style (e.g., "dark", "vibrant") of previous clips to suggest assets that maintain aesthetic continuity.

## 3. Predictive Harvesting (Smart Scraper)
**Goal**: Anticipate user needs by proactively populating the database.

*   **Trend Analysis**: Analyze the most frequent search terms and generated scripts from the last 7 days.
*   **Gap Detection**: If the system detects high interest in a topic (e.g., "AI Robotics") but low asset availability in the unified DB, it automatically starts Artlist/YouTube scraping jobs in the background.

## 4. Automated B-Roll Sequence Engine
**Goal**: Move from single clip selection to creating logical "sequences".

*   **Storytelling Logic**: Implement predefined editing patterns (e.g., *Wide Shot -> Medium Shot -> Close Up*).
*   **Action Matching**: If the script mentions a specific action (e.g., "running"), search for clip sequences showing progression in that action.

## 5. Smart Deduplication & Resolution Upscaling
**Goal**: Optimize storage and guarantee maximum visual quality.

*   **Cross-Source Deduplication**: Use `phash` and embeddings to identify if the same asset exists both as a YouTube clip and Artlist asset.
*   **Auto-Selection**: In case of duplicates, the system automatically picks the highest resolution/bitrate version and marks others as "mirror".

---

## Infrastructure Status
All these features are now possible thanks to database consolidation:
- [x] Unified Database (`media.db.sqlite`)
- [x] Flexible schema (`metadata_json`)
- [x] Semantic Embedding support
- [x] Perceptual Hashing (phash) support

## 6. Generative Gap-Filling

Like Instagram with AI Backdrops

**Goal**: if you don't have the clip, you create it.
**Logic**: when Predictive Harvesting finds a gap (e.g., "drone shot of Tokyo at night" is missing), instead of just scraping, launch SDXL-Turbo or AnimateDiff to generate 3s of synthetic B-roll.
**Action**: save with source: "gen" and prompt. Use the same embedding to find it later.
**Impact**: database never empty.

## 7. Social Feedback Loop

Like Meta's ranking

**Goal**: PipelineGen learns from what works.
**Logic**: every exported video receives metrics (watch time, CTR). Save them in a `performance` table.
**Action**: fine-tune your scoring: clips used in high-retention videos get a permanent boost in the DB.
**Impact**: the system gets smarter every week, without manual retraining.

When PipelineGen sees that a long-form video has a retention spike between 4:12 and 4:27, the Harvester automatically extracts that segment, indexes it as a new asset type `short_candidate`, generates 3 hook variants with Generative Gap-Filling, and proposes them ready for Shorts.
