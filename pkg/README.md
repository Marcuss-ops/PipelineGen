# Packages (pkg)

The `pkg` directory contains reusable, standalone packages that are independent of the PipelineGen domain logic. These packages are designed to be general-purpose utilities that could theoretically be used in other projects.

## Core Packages

- **config**: Centralized configuration management using YAML and environment variables.
- **logger**: Structured logging based on `uber-go/zap`.
- **models**: Shared data structures used throughout the application.
- **drive**: Utilities for interacting with the Google Drive API.
- **media**: Specialized media processing utilities (FFmpeg wrappers, downloaders).
- **sqlutil**: Helpers for SQL database operations and error handling.
- **hashutil**: Cryptographic and perceptual hashing utilities.
- **llmjson**: Utilities for extracting and validating JSON data from LLM responses.
- **security**: Authentication and authorization helpers.
- **executil**: Safe execution of external shell commands.

## General Utilities

- **apiutil**: Helpers for HTTP API responses and error mapping.
- **pathutil**: Safe file and directory path manipulation.
- **sliceutil**: Generic operations for slices.
- **textutil**: String manipulation and normalization.
- **timeutil**: Time formatting and calculation helpers.
- **termutil**: Terminal UI and progress reporting utilities.

## Philosophy

Packages in `pkg` should:
1.  **Be Stateless**: Most should not maintain internal state.
2.  **Avoid Internal Imports**: They should not import from the `internal/` directory.
3.  **Be Highly Tested**: As they are foundations for the rest of the system.
