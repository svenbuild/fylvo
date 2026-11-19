import { Box, IconButton, MenuItem, Skeleton, Stack, Tooltip, Typography } from "@mui/material";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Share, ShareMember } from "../../../../api/explorer.ts";
import { User } from "../../../../api/user.ts";
import { useAppDispatch } from "../../../../redux/hooks.ts";
import { setUserInfoCache } from "../../../../redux/globalStateSlice.ts";
import { loadUserInfo } from "../../../../redux/thunks/session.ts";
import SessionManager from "../../../../session";
import UserSearchInput from "../../../Admin/File/UserSearchInput.tsx";
import { DenseSelect, NoWrapTypography } from "../../../Common/StyledComponents.tsx";
import UserAvatar from "../../../Common/User/UserAvatar.tsx";
import Dismiss from "../../../Icons/Dismiss.tsx";
import People from "../../../Icons/People.tsx";

export interface ShareMembersProps {
  members: ShareMember[];
  onChange: (members: ShareMember[]) => void;
}

interface MemberRowProps {
  member: ShareMember;
  onChange: (member: ShareMember) => void;
  onRemove: () => void;
}

const MemberRow = ({ member, onChange, onRemove }: MemberRowProps) => {
  const { t } = useTranslation();
  const dispatch = useAppDispatch();
  const [user, setUser] = useState<User | undefined>(undefined);

  useEffect(() => {
    dispatch(loadUserInfo(member.user_id))
      .then(setUser)
      .catch(() => setUser({ id: member.user_id, nickname: member.user_id, created_at: "" }));
  }, [dispatch, member.user_id]);

  return (
    <Box sx={{ display: "flex", alignItems: "center", gap: 1, minHeight: 34 }}>
      <UserAvatar user={user} sx={{ width: 24, height: 24, fontSize: 12 }} />
      <Box sx={{ flexGrow: 1, minWidth: 0 }}>
        <NoWrapTypography variant="body2">{user ? user.nickname : <Skeleton width={80} />}</NoWrapTypography>
        {user?.email && (
          <NoWrapTypography variant="caption" color="text.secondary" sx={{ display: "block" }}>
            {user.email}
          </NoWrapTypography>
        )}
      </Box>
      <DenseSelect
        variant="outlined"
        size="small"
        value={member.can_edit ? "edit" : "read"}
        onChange={(e) => onChange({ ...member, can_edit: e.target.value == "edit" })}
        sx={{ fontSize: (theme) => theme.typography.body2.fontSize, flexShrink: 0 }}
      >
        <MenuItem dense value="read">
          {t("application:modals.memberRead")}
        </MenuItem>
        <MenuItem dense value="edit">
          {t("application:modals.memberEdit")}
        </MenuItem>
      </DenseSelect>
      <IconButton size="small" onClick={onRemove} aria-label={t("application:modals.removeMember")}>
        <Dismiss fontSize="small" />
      </IconButton>
    </Box>
  );
};

// Fylvo: marks a member share in share lists, with the number of people instead of link details.
export const ShareMembersBadge = ({ share }: { share: Share }) => {
  const { t } = useTranslation();
  return (
    <Tooltip title={t("application:modals.sharedWithPeopleTitle")}>
      <Typography
        variant="caption"
        color="text.secondary"
        sx={{ display: "inline-flex", alignItems: "center", gap: 0.5, flexShrink: 0 }}
      >
        <People sx={{ fontSize: 14 }} />
        {t("application:modals.peopleCount", { count: share.members?.length ?? 0 })}
      </Typography>
    </Tooltip>
  );
};

// Fylvo: the people a folder is shared with, each with the right to read or to edit.
const ShareMembers = ({ members, onChange }: ShareMembersProps) => {
  const { t } = useTranslation();
  const dispatch = useAppDispatch();

  const excludeIds = useMemo(() => {
    const ids = members.map((m) => m.user_id);
    const current = SessionManager.currentLoginOrNull()?.user.id;
    if (current) {
      ids.push(current);
    }
    return ids;
  }, [members]);

  const addUser = (user: User) => {
    if (members.some((m) => m.user_id == user.id)) {
      return;
    }
    // The search result already has the details, so the row does not need to load them again.
    dispatch(setUserInfoCache([user.id, user]));
    onChange([...members, { user_id: user.id, can_edit: false }]);
  };

  return (
    <Stack spacing={0.5} sx={{ mb: 1 }}>
      <UserSearchInput
        label={t("application:modals.addMember")}
        excludeIds={excludeIds}
        clearOnSelect
        onUserSelected={addUser}
      />
      {members.length == 0 && (
        <Typography variant="caption" color="text.secondary" sx={{ px: 0.5 }}>
          {t("application:modals.noMembers")}
        </Typography>
      )}
      {members.map((member) => (
        <MemberRow
          key={member.user_id}
          member={member}
          onChange={(changed) => onChange(members.map((m) => (m.user_id == changed.user_id ? changed : m)))}
          onRemove={() => onChange(members.filter((m) => m.user_id != member.user_id))}
        />
      ))}
    </Stack>
  );
};

export default ShareMembers;
