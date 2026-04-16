package handlers

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// UploadClip godoc
// @Summary Upload clip
// @Description Upload clip moments to Drive
// @Tags drive
// @Accept json
// @Produce json
// @Param request body drive.UploadClipRequest true "Upload clip request"
// @Success 200 {object} map[string]interface{}
// @Router /drive/upload-clip [post]
func (h *DriveHandler) UploadClip(c *gin.Context) {
	client, err := h.getClient(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	var req drive.UploadClipRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	if req.Topic == "" || len(req.Moments) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "topic and moments required"})
		return
	}

	group := req.Group
	if group == "" {
		group = drive.DetectGroupFromTopic(req.Topic)
	}
	groupName := drive.StockDriveGroups[group]

	// Create folder structure
	groupFolderID, err := client.GetOrCreateFolder(c.Request.Context(), groupName, "root")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Failed to create group folder"})
		return
	}

	topicFolderID, err := client.GetOrCreateFolder(c.Request.Context(), req.Topic, groupFolderID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Failed to create topic folder"})
		return
	}

	// Upload clips
	type UploadedClip struct {
		Filename  string `json:"filename"`
		FileID    string `json:"file_id"`
		URL       string `json:"url"`
		Timestamp string `json:"timestamp"`
	}

	var uploaded []UploadedClip
	for idx, moment := range req.Moments {
		desc := fmt.Sprintf(`# Clip: %s

## Timestamp
%s - %s

## Text
%s

## Details
- Duration: %.0fs
- Score: %.0f
- Topic: %s
- Video: %s
- URL: %s

## Tags
#%s #clip #stock
`, req.Topic, moment.Start, moment.End, moment.Text, moment.Duration, moment.Score,
			req.Topic, req.VideoTitle, req.VideoURL,
			strings.ReplaceAll(strings.ToLower(req.Topic), " ", "_"))

		ts := strings.NewReplacer(":", "", ".", "").Replace(moment.Start)
		filename := fmt.Sprintf("clip_%d_%s.txt", idx+1, ts)

		// Create temp file
		tempPath := filepath.Join(os.TempDir(), filename)
		if err := os.WriteFile(tempPath, []byte(desc), 0644); err != nil {
			logger.Warn("Failed to create temp file", zap.Error(err))
			continue
		}

		fileID, err := client.UploadFile(c.Request.Context(), tempPath, topicFolderID, filename)
		os.Remove(tempPath)

		if err != nil {
			logger.Warn("Failed to upload clip", zap.Error(err))
			continue
		}

		uploaded = append(uploaded, UploadedClip{
			Filename:  filename,
			FileID:    fileID,
			URL:       drive.GetDriveLink(fileID),
			Timestamp: fmt.Sprintf("%s - %s", moment.Start, moment.End),
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":         true,
		"uploaded":   uploaded,
		"count":      len(uploaded),
		"group":      groupName,
		"topic":      req.Topic,
		"topic_link": drive.GetFolderLink(topicFolderID),
	})
}

// DownloadAndUploadClip godoc
// @Summary Download from YouTube and upload to Drive
// @Description Download clip from YouTube and upload to Drive
// @Tags drive
// @Accept json
// @Produce json
// @Param request body drive.DownloadUploadClipRequest true "Download/upload request"
// @Success 200 {object} map[string]interface{}
// @Router /drive/download-and-upload-clip [post]
func (h *DriveHandler) DownloadAndUploadClip(c *gin.Context) {
	client, err := h.getClient(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	var req drive.DownloadUploadClipRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	topic := req.Topic
	if topic == "" {
		topic = "clip"
	}

	group := req.Group
	if group == "" {
		group = drive.DetectGroupFromTopic(topic)
	}
	groupName := drive.StockDriveGroups[group]

	// Create folder structure
	groupFolderID, err := client.GetOrCreateFolder(c.Request.Context(), groupName, "root")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Failed to create group folder"})
		return
	}

	topicFolderID, err := client.GetOrCreateFolder(c.Request.Context(), topic, groupFolderID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Failed to create topic folder"})
		return
	}

	// Download from YouTube using yt-dlp
	outputTemplate := filepath.Join(os.TempDir(), "clip_%(id)s.%(ext)s")

	cmd := exec.CommandContext(c.Request.Context(), "yt-dlp",
		"-f", "bestvideo[height<=1080]+bestaudio/best[height<=1080]",
		"--download-sections", fmt.Sprintf("*%s-%s", req.StartTime, req.EndTime),
		"--no-playlist",
		"-o", outputTemplate,
		req.YouTubeURL,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		logger.Error("yt-dlp failed", zap.String("output", string(output)), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": fmt.Sprintf("Download failed: %s", err)})
		return
	}

	// Find downloaded file
	files, _ := filepath.Glob(filepath.Join(os.TempDir(), "clip_*"))
	if len(files) == 0 {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "No downloaded file found"})
		return
	}

	videoPath := files[0]
	defer os.Remove(videoPath)

	videoFilename := filepath.Base(videoPath)

	// Upload video
	fileID, err := client.UploadFile(c.Request.Context(), videoPath, topicFolderID, videoFilename)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	// Create description file
	textContent := fmt.Sprintf(`# Clip: %s

## YouTube URL
%s

## Timestamp
%s - %s

## Topic
%s

## Group
%s

---
Generated by Studio App
`, topic, req.YouTubeURL, req.StartTime, req.EndTime, topic, groupName)

	textFilename := fmt.Sprintf("clip_%s_info.txt", strings.NewReplacer(":", "").Replace(req.StartTime))
	tempPath := filepath.Join(os.TempDir(), textFilename)
	os.WriteFile(tempPath, []byte(textContent), 0644)
	defer os.Remove(tempPath)

	textFileID, _ := client.UploadFile(c.Request.Context(), tempPath, topicFolderID, textFilename)

	type UploadedItem struct {
		Type     string `json:"type"`
		Filename string `json:"filename"`
		FileID   string `json:"file_id"`
		URL      string `json:"url"`
	}

	uploaded := []UploadedItem{
		{Type: "video", Filename: videoFilename, FileID: fileID, URL: drive.GetDriveLink(fileID)},
	}

	if textFileID != "" {
		uploaded = append(uploaded, UploadedItem{
			Type: "description", Filename: textFilename, FileID: textFileID, URL: drive.GetDriveLink(textFileID),
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":         true,
		"uploaded":   uploaded,
		"count":      len(uploaded),
		"group":      groupName,
		"topic":      topic,
		"topic_link": drive.GetFolderLink(topicFolderID),
	})
}
