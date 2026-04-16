package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"velox/go-master/internal/upload/drive"
)

// FoldersTree godoc
// @Summary List folders tree
// @Description Get hierarchical tree of Drive folders
// @Tags drive
// @Produce json
// @Param folder_id query string false "Parent folder ID"
// @Param max_depth query int false "Max recursion depth" default(2)
// @Param max_items query int false "Max items per level" default(50)
// @Success 200 {object} map[string]interface{}
// @Router /drive/folders-tree [get]
func (h *DriveHandler) FoldersTree(c *gin.Context) {
	client, err := h.getClient(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	folderID := c.Query("folder_id")
	maxDepth := parseIntQuery(c, "max_depth", 2)
	maxItems := parseIntQuery(c, "max_items", 50)

	opts := drive.ListFoldersOptions{
		ParentID: folderID,
		MaxDepth: maxDepth,
		MaxItems: maxItems,
	}

	folders, err := client.ListFolders(c.Request.Context(), opts)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":           true,
		"root_folders": folders,
		"total":        len(folders),
	})
}

// FolderContent godoc
// @Summary Get folder content
// @Description List files and subfolders in a Drive folder
// @Tags drive
// @Produce json
// @Param folder_id query string false "Folder ID"
// @Param folder_name query string false "Folder name (alternative to folder_id)"
// @Param page_size query int false "Page size" default(100)
// @Success 200 {object} map[string]interface{}
// @Router /drive/folder-content [get]
func (h *DriveHandler) FolderContent(c *gin.Context) {
	client, err := h.getClient(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	folderID := c.Query("folder_id")
	folderName := c.Query("folder_name")

	if folderID == "" && folderName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "folder_id or folder_name required"})
		return
	}

	// Find folder by name if needed
	if folderID == "" && folderName != "" {
		folder, err := client.GetFolderByName(c.Request.Context(), folderName, "")
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": fmt.Sprintf("Folder '%s' not found", folderName)})
			return
		}
		folderID = folder.ID
		folderName = folder.Name
	}

	content, err := client.GetFolderContent(c.Request.Context(), folderID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":               true,
		"folder_id":        content.FolderID,
		"folder_name":      folderName,
		"subfolders":       content.Subfolders,
		"files":            content.Files,
		"total_subfolders": content.TotalFolders,
		"total_files":      content.TotalFiles,
	})
}

// CreateFolder godoc
// @Summary Create a folder
// @Description Create a new folder in Google Drive
// @Tags drive
// @Accept json
// @Produce json
// @Param request body drive.CreateFolderRequest true "Create folder request"
// @Success 200 {object} map[string]interface{}
// @Router /drive/create-folder [post]
func (h *DriveHandler) CreateFolder(c *gin.Context) {
	client, err := h.getClient(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	var req drive.CreateFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	parentID := req.ParentID
	if parentID == "" {
		parentID = "root"
	}

	folderID, err := client.CreateFolder(c.Request.Context(), req.Name, parentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":          true,
		"folder_id":   folderID,
		"folder_name": req.Name,
	})
}

// CreateFolderStructure godoc
// @Summary Create folder structure
// @Description Create group/topic folder structure
// @Tags drive
// @Accept json
// @Produce json
// @Param request body drive.FolderStructureRequest true "Folder structure request"
// @Success 200 {object} map[string]interface{}
// @Router /drive/create-folder-structure [post]
func (h *DriveHandler) CreateFolderStructure(c *gin.Context) {
	client, err := h.getClient(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	var req drive.FolderStructureRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	group := req.Group
	if group == "" {
		group = drive.DetectGroupFromTopic(req.Topic)
	}
	groupName := drive.StockDriveGroups[group]

	groupFolderID, err := client.GetOrCreateFolder(c.Request.Context(), groupName, "root")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	topicFolderID, err := client.GetOrCreateFolder(c.Request.Context(), req.Topic, groupFolderID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":              true,
		"group":           groupName,
		"group_folder_id": groupFolderID,
		"topic":           req.Topic,
		"topic_folder_id": topicFolderID,
		"group_link":      drive.GetFolderLink(groupFolderID),
		"topic_link":      drive.GetFolderLink(topicFolderID),
	})
}
