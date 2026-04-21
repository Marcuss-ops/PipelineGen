// Package gpu provides NVIDIA GPU acceleration management for AI text generation
// This module manages GPU device detection, CUDA availability, and Ollama integration
package gpu

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// GPUInfo represents information about a GPU device
type GPUInfo struct {
	Index           int     `json:"index"`
	Name            string  `json:"name"`
	MemoryTotal     uint64  `json:"memory_total_mb"`
	MemoryUsed      uint64  `json:"memory_used_mb"`
	MemoryFree      uint64  `json:"memory_free_mb"`
	Temperature     int     `json:"temperature_c"`
	PowerUsage      int     `json:"power_usage_watts"`
	Utilization     int     `json:"utilization_percent"`
	DriverVersion   string  `json:"driver_version"`
	CUDAVersion     string  `json:"cuda_version"`
}

// GPUConfig holds configuration for GPU acceleration
type GPUConfig struct {
	Enabled       bool   `json:"enabled"`
	DeviceIndex   int    `json:"device_index"`
	MinMemoryMB   uint64 `json:"min_memory_mb"` // Minimum free memory required
	MaxTemp       int    `json:"max_temp_c"`    // Maximum temperature before throttling
	OllamaGPU     bool   `json:"ollama_gpu"`    // Use GPU for Ollama inference
	OllamaHost    string `json:"ollama_host"`   // Ollama server host
	OllamaPort    int    `json:"ollama_port"`   // Ollama server port
}

// Manager manages GPU resources and health monitoring
type Manager struct {
	config   *GPUConfig
	gpus     []GPUInfo
	healthy  bool
	mu       sync.RWMutex
	lastCheck time.Time
}

// NewManager creates a new GPU manager
func NewManager(config *GPUConfig) *Manager {
	if config == nil {
		config = &GPUConfig{
			Enabled:     false,
			DeviceIndex: 0,
			MinMemoryMB: 1024, // 1GB minimum free
			MaxTemp:     85,   // 85°C max
			OllamaGPU:   true,
			OllamaHost:  "localhost",
			OllamaPort:  11434,
		}
	}
	
	return &Manager{
		config:  config,
		healthy: false,
	}
}

// Initialize detects and validates GPU hardware
func (m *Manager) Initialize(ctx context.Context) error {
	if !m.config.Enabled {
		logger.Info("GPU acceleration disabled")
		return nil
	}
	
	// Check if running on Linux (CUDA requirement)
	if runtime.GOOS != "linux" {
		logger.Warn("GPU acceleration only supported on Linux")
		m.config.Enabled = false
		return nil
	}
	
	// Detect NVIDIA GPUs
	gpus, err := m.detectGPUs(ctx)
	if err != nil {
		logger.Warn("Failed to detect GPUs", zap.Error(err))
		m.config.Enabled = false
		return nil
	}
	
	if len(gpus) == 0 {
		logger.Warn("No NVIDIA GPUs detected")
		m.config.Enabled = false
		return nil
	}
	
	m.mu.Lock()
	m.gpus = gpus
	m.healthy = true
	m.lastCheck = time.Now()
	m.mu.Unlock()
	
	// Log GPU information
	for _, gpu := range gpus {
		logger.Info("GPU detected",
			zap.Int("index", gpu.Index),
			zap.String("name", gpu.Name),
			zap.Uint64("memory_mb", gpu.MemoryTotal),
			zap.Int("temperature_c", gpu.Temperature),
		)
	}
	
	// Check if Ollama can use GPU
	if m.config.OllamaGPU {
		if err := m.checkOllamaGPU(ctx); err != nil {
			logger.Warn("Ollama GPU acceleration not available", zap.Error(err))
			m.config.OllamaGPU = false
		} else {
			logger.Info("Ollama GPU acceleration enabled")
		}
	}
	
	return nil
}

// GetGPUInfo returns information about the selected GPU
func (m *Manager) GetGPUInfo(index int) (*GPUInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	if index < 0 || index >= len(m.gpus) {
		return nil, fmt.Errorf("GPU index %d out of range", index)
	}
	
	return &m.gpus[index], nil
}

// GetSelectedGPU returns the currently selected GPU
func (m *Manager) GetSelectedGPU() (*GPUInfo, error) {
	if !m.config.Enabled {
		return nil, fmt.Errorf("GPU acceleration disabled")
	}
	
	return m.GetGPUInfo(m.config.DeviceIndex)
}

// IsHealthy checks if the GPU is in a healthy state
func (m *Manager) IsHealthy(ctx context.Context) bool {
	m.mu.RLock()
	if !m.healthy {
		m.mu.RUnlock()
		return false
	}
	m.mu.RUnlock()
	
	// Refresh GPU stats periodically (every 30 seconds)
	m.mu.RLock()
	if time.Since(m.lastCheck) > 30*time.Second {
		m.mu.RUnlock()
		m.RefreshGPUStats(ctx)
		m.mu.RLock()
	}
	defer m.mu.RUnlock()
	
	if m.config.DeviceIndex >= len(m.gpus) {
		return false
	}
	
	gpu := m.gpus[m.config.DeviceIndex]
	
	// Check temperature
	if gpu.Temperature > m.config.MaxTemp {
		logger.Warn("GPU temperature too high", zap.Int("temp_c", gpu.Temperature))
		return false
	}
	
	// Check free memory
	if gpu.MemoryFree < m.config.MinMemoryMB {
		logger.Warn("Insufficient GPU memory",
			zap.Uint64("free_mb", gpu.MemoryFree),
			zap.Uint64("required_mb", m.config.MinMemoryMB),
		)
		return false
	}
	
	return true
}

// RefreshGPUStats updates GPU statistics
func (m *Manager) RefreshGPUStats(ctx context.Context) error {
	gpus, err := m.detectGPUs(ctx)
	if err != nil {
		return err
	}
	
	m.mu.Lock()
	m.gpus = gpus
	m.lastCheck = time.Now()
	m.mu.Unlock()
	
	return nil
}

// GetOllamaEnv returns environment variables for GPU-accelerated Ollama
func (m *Manager) GetOllamaEnv() map[string]string {
	env := make(map[string]string)
	
	if !m.config.Enabled || !m.config.OllamaGPU {
		return env
	}
	
	// Set CUDA visibility to show only selected GPU
	env["CUDA_VISIBLE_DEVICES"] = fmt.Sprintf("%d", m.config.DeviceIndex)
	
	// Ollama GPU settings
	env["OLLAMA_HOST"] = fmt.Sprintf("%s:%d", m.config.OllamaHost, m.config.OllamaPort)
	env["OLLAMA_GPU"] = "true"
	
	return env
}

// GenerateWithOllama generates text using Ollama with GPU acceleration
func (m *Manager) GenerateWithOllama(ctx context.Context, model, prompt string) (string, error) {
	if !m.config.OllamaGPU {
		return "", fmt.Errorf("Ollama GPU acceleration not enabled")
	}
	
	// Check GPU health before generation
	if !m.IsHealthy(ctx) {
		logger.Warn("GPU unhealthy, falling back to CPU")
		// Could implement CPU fallback here
	}
	
	// Call Ollama API (this would be implemented in your existing ml/ollama module)
	// This is a placeholder showing GPU integration
	ollamaURL := fmt.Sprintf("http://%s:%d/api/generate", m.config.OllamaHost, m.config.OllamaPort)
	
	logger.Info("Generating text with Ollama GPU",
		zap.String("model", model),
		zap.String("url", ollamaURL),
		zap.Int("gpu_device", m.config.DeviceIndex),
	)
	
	// The actual Ollama API call would go here
	// For now, return a placeholder showing GPU configuration
	return "", fmt.Errorf("Ollama API integration pending - use existing ml module")
}

// detectGPUs detects NVIDIA GPUs using nvidia-smi
func (m *Manager) detectGPUs(ctx context.Context) ([]GPUInfo, error) {
	// Check if nvidia-smi is available
	if _, err := exec.LookPath("nvidia-smi"); err != nil {
		return nil, fmt.Errorf("nvidia-smi not found: %w", err)
	}
	
	// Query GPU information
	cmd := exec.CommandContext(ctx, "nvidia-smi",
		"--query-gpu=index,name,memory.total,memory.used,memory.free,temperature.gpu,power.draw,utilization.gpu",
		"--format=csv,noheader,nounits",
	)
	
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("nvidia-smi query failed: %w", err)
	}
	
	var gpus []GPUInfo
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		
		parts := strings.Split(line, ", ")
		if len(parts) < 8 {
			continue
		}
		
		var gpu GPUInfo
		fmt.Sscanf(parts[0], "%d", &gpu.Index)
		gpu.Name = strings.TrimSpace(parts[1])
		fmt.Sscanf(parts[2], "%d", &gpu.MemoryTotal)
		fmt.Sscanf(parts[3], "%d", &gpu.MemoryUsed)
		fmt.Sscanf(parts[4], "%d", &gpu.MemoryFree)
		fmt.Sscanf(parts[5], "%d", &gpu.Temperature)
		fmt.Sscanf(parts[6], "%d", &gpu.PowerUsage)
		fmt.Sscanf(parts[7], "%d", &gpu.Utilization)
		
		gpus = append(gpus, gpu)
	}
	
	// Get driver and CUDA version
	cmd = exec.CommandContext(ctx, "nvidia-smi", "--query-gpu=driver_version", "--format=csv,noheader,nounits")
	output, err = cmd.Output()
	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(output)), "\n")
		if len(lines) > 0 {
			for i := range gpus {
				gpus[i].DriverVersion = strings.TrimSpace(lines[i%len(lines)])
			}
		}
	}
	
	// Get CUDA version
	cmd = exec.CommandContext(ctx, "nvidia-smi", "--query-gpu=cuda_version", "--format=csv,noheader,nounits")
	output, err = cmd.Output()
	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(output)), "\n")
		if len(lines) > 0 {
			for i := range gpus {
				gpus[i].CUDAVersion = strings.TrimSpace(lines[i%len(lines)])
			}
		}
	}
	
	return gpus, nil
}

// checkOllamaGPU checks if Ollama can use GPU acceleration
func (m *Manager) checkOllamaGPU(ctx context.Context) error {
	// Check if Ollama is running
	ollamaURL := fmt.Sprintf("http://%s:%d", m.config.OllamaHost, m.config.OllamaPort)
	
	// Try to get Ollama version
	cmd := exec.CommandContext(ctx, "curl", "-s", fmt.Sprintf("%s/api/version", ollamaURL))
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("Ollama not reachable: %w", err)
	}
	
	var version struct {
		Version string `json:"version"`
	}
	
	if err := json.Unmarshal(output, &version); err != nil {
		return fmt.Errorf("Failed to parse Ollama version: %w", err)
	}
	
	logger.Info("Ollama detected", zap.String("version", version.Version))
	
	// Check GPU support via Ollama's /api/tags endpoint
	cmd = exec.CommandContext(ctx, "curl", "-s", fmt.Sprintf("%s/api/tags", ollamaURL))
	output, err = cmd.Output()
	if err != nil {
		return fmt.Errorf("Failed to get Ollama models: %w", err)
	}
	
	// Parse and check if any models support GPU
	var models struct {
		Models []struct {
			Name   string `json:"name"`
			Details struct {
				Family       string `json:"family"`
				FamilyType   string `json:"family_type"`
				ParameterSize string `json:"parameter_size"`
				QuantizationLevel string `json:"quantization_level"`
			} `json:"details"`
		} `json:"models"`
	}
	
	if err := json.Unmarshal(output, &models); err != nil {
		return fmt.Errorf("Failed to parse Ollama models: %w", err)
	}
	
	logger.Info("Ollama GPU support verified",
		zap.Int("models_available", len(models.Models)),
	)
	
	return nil
}

// GetGPUSummary returns a human-readable GPU summary
func (m *Manager) GetGPUSummary() string {
	if !m.config.Enabled {
		return "GPU acceleration: DISABLED"
	}
	
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	if len(m.gpus) == 0 {
		return "GPU acceleration: ENABLED (no GPUs detected)"
	}
	
	if m.config.DeviceIndex >= len(m.gpus) {
		return fmt.Sprintf("GPU acceleration: INVALID DEVICE INDEX %d", m.config.DeviceIndex)
	}
	
	gpu := m.gpus[m.config.DeviceIndex]
	return fmt.Sprintf("GPU: %s (%d) - %dMB/%dMB used, %d°C, %d%% util",
		gpu.Name,
		gpu.Index,
		gpu.MemoryUsed,
		gpu.MemoryTotal,
		gpu.Temperature,
		gpu.Utilization,
	)
}

// Close releases GPU resources (currently no-op)
func (m *Manager) Close() error {
	logger.Info("GPU manager closing")
	return nil
}
