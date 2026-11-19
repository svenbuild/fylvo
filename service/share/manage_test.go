package share

import (
	"testing"

	"github.com/gin-gonic/gin/binding"
)

// Fylvo: nonsense values for a share are refused before they reach the file manager.
func TestShareCreateServiceRefusesNegativeValues(t *testing.T) {
	cases := map[string]ShareCreateService{
		"downloads":    {Uri: "cloudreve://my/a", RemainDownloads: -1},
		"expire":       {Uri: "cloudreve://my/a", Expire: -60},
		"huge expire":  {Uri: "cloudreve://my/a", Expire: 1 << 40},
		"upload limit": {Uri: "cloudreve://my/a", UploadLimit: -1},
	}
	for name, service := range cases {
		if err := binding.Validator.ValidateStruct(&service); err == nil {
			t.Errorf("negative %s was accepted", name)
		}
	}

	valid := ShareCreateService{Uri: "cloudreve://my/a", RemainDownloads: 5, Expire: 3600, UploadLimit: 1 << 30}
	if err := binding.Validator.ValidateStruct(&valid); err != nil {
		t.Errorf("valid share refused: %v", err)
	}
}
