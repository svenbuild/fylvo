package types

// Fylvo: shared folders. A member-only share names the users who may open it.

// IsMemberOnly reports whether the share is open to listed members only.
func (p *ShareProps) IsMemberOnly() bool {
	return p != nil && p.MemberOnly
}

// AllowsUpload reports whether the visitors of the link may add files to the shared folder.
// Members of a shared folder have rights of their own, the link option does not apply to them.
func (p *ShareProps) AllowsUpload() bool {
	return p != nil && p.AllowUpload && !p.MemberOnly
}

// HidesContent reports whether the visitors of an upload link are kept from seeing what the
// shared folder holds.
func (p *ShareProps) HidesContent() bool {
	return p.AllowsUpload() && p.HideContent
}

// Member returns the entry of the given user on the member list, or nil if the share
// is not member-only or the user is not listed.
func (p *ShareProps) Member(userID int) *ShareMember {
	if !p.IsMemberOnly() {
		return nil
	}

	for i := range p.Members {
		if p.Members[i].UserID == userID {
			return &p.Members[i]
		}
	}

	return nil
}
