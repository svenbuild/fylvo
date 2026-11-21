import { Box, List, ListItem, ListItemText, Typography, useTheme } from "@mui/material";
import { useTranslation } from "react-i18next";
import { ActivityEntry } from "../../../api/activity.ts";
import { SecondaryButton } from "../StyledComponents.tsx";
import TimeBadge from "../TimeBadge.tsx";
import { activityText, deviceName, isFailedSignIn } from "./activityText.ts";

export interface ActivityListProps {
  entries: ActivityEntry[];
  // Show address and device below each entry (own sign-ins).
  withDevice?: boolean;
  loading?: boolean;
  hasMore?: boolean;
  onLoadMore?: () => void;
}

// ActivityList shows entries of the activity log, newest first (Fylvo).
const ActivityList = ({ entries, withDevice, loading, hasMore, onLoadMore }: ActivityListProps) => {
  const { t } = useTranslation("application");
  const theme = useTheme();

  if (!loading && entries.length === 0 && !hasMore) {
    return (
      <Typography variant="body2" color="text.secondary" sx={{ py: 1 }}>
        {t("activity.empty")}
      </Typography>
    );
  }

  return (
    <Box>
      <List disablePadding dense>
        {entries.map((entry) => {
          const device = withDevice ? deviceName(t, entry) : "";
          const details = [device, withDevice ? entry.ip : ""].filter(Boolean).join(" · ");
          return (
            <ListItem key={entry.id} disableGutters sx={{ alignItems: "flex-start", py: 0.5 }}>
              <ListItemText
                primary={activityText(t, entry)}
                primaryTypographyProps={{
                  variant: "body2",
                  sx: {
                    wordBreak: "break-word",
                    color: isFailedSignIn(entry) ? theme.palette.error.main : undefined,
                  },
                }}
                secondary={
                  <>
                    <TimeBadge datetime={entry.created_at} variant="caption" />
                    {details && (
                      <Typography component="span" variant="caption">
                        {" · " + details}
                      </Typography>
                    )}
                  </>
                }
                secondaryTypographyProps={{ component: "div", color: "text.secondary" }}
              />
            </ListItem>
          );
        })}
      </List>
      {hasMore && (
        <SecondaryButton variant="contained" size="small" disabled={loading} onClick={onLoadMore} sx={{ mt: 1 }}>
          {t("activity.loadMore")}
        </SecondaryButton>
      )}
    </Box>
  );
};

export default ActivityList;
