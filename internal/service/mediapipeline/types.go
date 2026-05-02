package mediapipeline

import (
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/service/drivedestination"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/media/downloader"
	"velox/go-master/pkg/media/ffmpeg"
)

type ProcessingMode string

const (
	ProcessingNone      ProcessingMode = "none"
	ProcessingNormalize ProcessingMode = "normalize"
	ProcessingCustom    ProcessingMode = "custom"
)

type PipelineRequest struct {
	Source      string
	MediaType   string
	Category    string
	URLs        []SourceURL
	Segments    []SegmentSpec
	Destination DestinationSpec
	Processing  ProcessingSpec
	UploadDrive bool
	SaveDB      bool
	Tags        []string
	Group       string
}

type SourceURL struct {
	URL  string
	Name string
}

type SegmentSpec struct {
	SourceURL string
	Start     string
	End       string
	Name      string
	Tags      []string
}

type ProcessingSpec struct {
	Mode         ProcessingMode
	Normalize    bool
	JoinInputs   bool
	Duration     int
	Width        int
	Height       int
	FPS          int
	OutputName   string
}

type DestinationSpec struct {
	Group           string
	FolderID        string
	FolderPath      string
	SubfolderName   string
	CreateSubfolder bool
}

type PipelineItem struct {
	ID           string
	Name         string
	SourceURL    string
	LocalPath    string
	ProcessedPath string
	DriveLink    string
	FileHash     string
	Status       string
	Error        string
}

type PipelineResponse struct {
	Items []*WorkItem
}

type WorkItem struct {
	ID            string
	Name          string
	SourceURL     string
	SegmentSpec   *SegmentSpec
	LocalPath     string
	ProcessedPath string
	DriveLink     string
	FileHash      string
	Status        string
	Error         string
	Tags          []string
}

func (item *WorkItem) Fail(err error) {
	item.Status = "failed"
	item.Error = err.Error()
}

type ResolvedDestination struct {
	FolderID   string
	FolderPath string
	Group      string
}

type Service struct {
	ytdlpDownloader  *downloader.YTDLPDownloader
	ffmpegProcessor  *ffmpeg.Processor
	driveUploader    *drive.Uploader
	driveDestination *drivedestination.Service
	clipsRepo        *clips.Repository
	idGenerator      idGenerator
	downloadOutputDir string
	processOutputDir  string
}

type idGenerator interface {
	GenerateID(sourceURL string, req *PipelineRequest) string
}
