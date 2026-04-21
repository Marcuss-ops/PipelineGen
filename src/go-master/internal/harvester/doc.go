// Package harvester provides automated content discovery and harvesting services.
//
// It is responsible for:
// 1. Discovery: Monitoring YouTube channels, social media, or search queries for new relevant content.
// 2. Evaluation: Filtering discovered content based on metadata (views, date, duration).
// 3. Extraction: Downloading and processing video segments into the local stock database.
// 4. Scheduling: Running periodic harvesting cycles to keep the library fresh.
//
// The harvester acts as an autonomous agent that feeds the internal clip library,
// ensuring a constant supply of B-roll and interview footage for the script pipeline.
package harvester
