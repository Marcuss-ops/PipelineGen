package config

// BooksConfig holds settings for the book summarization/processing service.
type BooksConfig struct {
	Enabled       bool   `yaml:"enabled" default:"true"`
	ScriptPath    string `yaml:"script_path" default:"scripts/bridges/book_summarizer.py"`
	PythonBin     string `yaml:"python_bin" default:"python3"`
	DefaultModel  string `yaml:"default_model" default:"gemma4:e4b"`
	OllamaURL     string `yaml:"ollama_url" default:"http://127.0.0.1:11434"`
	PagesPerChunk int    `yaml:"pages_per_chunk" default:"4"`
	ChunkSize     int    `yaml:"chunk_size" default:"12000"`
}
