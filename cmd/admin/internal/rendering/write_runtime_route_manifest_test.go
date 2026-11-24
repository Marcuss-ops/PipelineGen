package rendering

import "github.com/gin-gonic/gin"

func writeRuntimeRouteManifest(path string, routes []gin.RouteInfo) error {
	data, err := runtimeRouteManifestData(routes)
	if err != nil {
		return err
	}
	return writeAtomicFile(path, data, 0644)
}
