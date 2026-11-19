package manager

import (
	"context"
	"fmt"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/samber/lo"
)

// Fylvo: shared folders. A member-only share names the users who may open the folder.

// MaxShareMembers is the largest number of members a shared folder can have.
const MaxShareMembers = 50

// memberOnlyShare reports whether the share to save is member-only. That is decided when a
// share is created and stays: a link in somebody's hands must not start to open a folder
// meant for members, and a member-only share must not become a link when its last member
// is removed or when a client that knows nothing about members saves it.
func (l *manager) memberOnlyShare(file fs.File, existed *ent.Share, args *CreateShareArgs) (bool, error) {
	if existed != nil {
		if args.MemberOnly && !existed.Props.IsMemberOnly() {
			return false, serializer.NewError(serializer.CodeParamErr, "a share link cannot be turned into a shared folder", nil)
		}

		return existed.Props.IsMemberOnly(), nil
	}

	if args.MemberOnly && file.Type() != types.FileTypeFolder {
		return false, serializer.NewError(serializer.CodeParamErr, "only a folder can be shared with members", nil)
	}

	return args.MemberOnly, nil
}

// validateShareMembers makes sure every member is another user and is listed once, and that
// new members are active users. Members the share has already (known) are not looked up
// again: one of them being deactivated later must not block every change to the share.
func (l *manager) validateShareMembers(ctx context.Context, members, known []types.ShareMember) error {
	if len(members) > MaxShareMembers {
		return serializer.NewError(serializer.CodeParamErr, fmt.Sprintf("a shared folder can have at most %d members", MaxShareMembers), nil)
	}

	seen := make(map[int]struct{}, len(members))
	for _, member := range members {
		if _, ok := seen[member.UserID]; ok || member.UserID == l.user.ID {
			return serializer.NewError(serializer.CodeParamErr, "a member is listed twice or is the owner", nil)
		}
		seen[member.UserID] = struct{}{}

		if lo.ContainsBy(known, func(k types.ShareMember) bool { return k.UserID == member.UserID }) {
			continue
		}

		if _, err := l.dep.UserClient().GetActiveByID(ctx, member.UserID); err != nil {
			return serializer.NewError(serializer.CodeParamErr, "a member is not an active user", err)
		}
	}

	return nil
}
