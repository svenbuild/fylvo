package types

// Fylvo: shared folders. A member-only share names the users who may open it.

// IsMemberOnly reports whether the share is open to listed members only.
func (p *ShareProps) IsMemberOnly() bool {
	return p != nil && p.MemberOnly
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
