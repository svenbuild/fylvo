package explorer

import (
	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

type (
	PinFileService struct {
		Uri  string `json:"uri" binding:"required"`
		Name string `json:"name"`
	}
	PinFileParameterCtx struct{}
)

// PinFileService pins new uri to sidebar
func (service *PinFileService) PinFile(c *gin.Context) error {
	dep := dependency.FromContext(c)
	user := inventory.UserFromContext(c)
	userClient := dep.UserClient()

	uri, err := fs.NewUriFromString(service.Uri)
	if err != nil {
		return serializer.NewError(serializer.CodeParamErr, "unknown uri", err)
	}

	uriStr := uri.String()
	settings, err := userClient.UpdateSettings(c, user.ID, func(s *types.UserSetting) error {
		for _, pin := range s.Pined {
			if pin.Uri == uriStr {
				if pin.Name != service.Name {
					return serializer.NewError(serializer.CodeObjectExist, "uri already pinned with different name", nil)
				}

				return nil
			}
		}

		s.Pined = append(s.Pined, types.PinedFile{
			Uri:  uriStr,
			Name: service.Name,
		})
		return nil
	})
	if err != nil {
		return serializer.KeepAppError(err, serializer.CodeDBError, "failed to save settings")
	}

	user.Settings = settings
	return nil
}

// UnpinFile removes uri from sidebar
func (service *PinFileService) UnpinFile(c *gin.Context) error {
	dep := dependency.FromContext(c)
	user := inventory.UserFromContext(c)
	userClient := dep.UserClient()

	uri, err := fs.NewUriFromString(service.Uri)
	if err != nil {
		return serializer.NewError(serializer.CodeParamErr, "unknown uri", err)
	}

	uriStr := uri.String()
	settings, err := userClient.UpdateSettings(c, user.ID, func(s *types.UserSetting) error {
		s.Pined = lo.Filter(s.Pined, func(pin types.PinedFile, index int) bool {
			return pin.Uri != uriStr
		})
		return nil
	})
	if err != nil {
		return serializer.KeepAppError(err, serializer.CodeDBError, "failed to save settings")
	}

	user.Settings = settings
	return nil
}
