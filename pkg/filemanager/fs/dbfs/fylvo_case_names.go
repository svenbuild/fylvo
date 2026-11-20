package dbfs

import (
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
)

// Fylvo: names that differ only in upper and lower case are refused where they are written
// (inventory/fylvo_case_names.go). writeError is the answer to a failed write of a name: the
// same name is "existed" as before, another spelling of a name keeps its own code (40905),
// anything else is a database error.
func writeError(err error, msg string) error {
	if inventory.IsNameCaseTaken(err) {
		return err
	}
	if ent.IsConstraintError(err) {
		return fs.ErrFileExisted.WithError(err)
	}
	return serializer.NewError(serializer.CodeDBError, msg, err)
}
