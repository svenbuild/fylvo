import {
  Box,
  Chip,
  Container,
  Link,
  ListItemText,
  Stack,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  Typography,
  useTheme,
} from "@mui/material";
import { LoadingButton } from "@mui/lab";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { ActivityEntry, ActivityUser } from "../../../api/activity.ts";
import { getAdminActivity, getSettings, sendSetSetting } from "../../../api/api.ts";
import { useAppDispatch } from "../../../redux/hooks.ts";
import { activityText, actorName, deviceName, isFailedSignIn } from "../../Common/Activity/activityText.ts";
import { useActivityFeed } from "../../Common/Activity/useActivityFeed.ts";
import {
  DenseFilledTextField,
  DenseSelect,
  NoWrapTableCell,
  SecondaryButton,
  StyledTableContainerPaper,
} from "../../Common/StyledComponents.tsx";
import TimeBadge from "../../Common/TimeBadge.tsx";
import { SquareMenuItem } from "../../FileManager/ContextMenu/ContextMenu.tsx";
import ArrowSync from "../../Icons/ArrowSync.tsx";
import PageHeader from "../../Pages/PageHeader.tsx";
import AdminPage from "../Common/AdminPage.tsx";

const retentionKey = "activity_retention_days";
const groups = ["", "files", "shares", "account"];

// Fylvo: the whole activity log, with address and device, for the admin.
const ActivityLog = () => {
  const { t } = useTranslation("application");
  const theme = useTheme();
  const dispatch = useAppDispatch();
  const [group, setGroup] = useState("");
  const [account, setAccount] = useState<ActivityUser | undefined>(undefined);
  const [refresh, setRefresh] = useState(0);
  const [retention, setRetention] = useState("");
  const [savedRetention, setSavedRetention] = useState("");
  const [saving, setSaving] = useState(false);

  const feed = useActivityFeed(`${group}|${account?.id ?? ""}|${refresh}`, (next) =>
    dispatch(getAdminActivity({ next, group: group || undefined, account: account?.id })),
  );

  useEffect(() => {
    dispatch(getSettings({ keys: [retentionKey] })).then((res) => {
      setRetention(res[retentionKey] ?? "");
      setSavedRetention(res[retentionKey] ?? "");
    });
  }, []);

  const saveRetention = () => {
    setSaving(true);
    dispatch(sendSetSetting({ settings: { [retentionKey]: retention } }))
      .then(() => setSavedRetention(retention))
      .finally(() => setSaving(false));
  };

  const accountName = (u: ActivityUser) => u.nickname || u.email || t("activity.deletedAccount");

  const accountCell = (u?: ActivityUser) =>
    u ? (
      <Link component="button" variant="body2" underline="hover" onClick={() => setAccount(u)}>
        {accountName(u)}
      </Link>
    ) : (
      ""
    );

  const actorCell = (entry: ActivityEntry) => (entry.actor ? accountCell(entry.actor) : actorName(t, entry));

  return (
    <AdminPage>
      <Container maxWidth="xl">
        <PageHeader title={t("dashboard:nav.activity")} />
        <Stack direction="row" spacing={1} sx={{ mb: 2, alignItems: "center" }}>
          <SecondaryButton
            onClick={() => setRefresh((r) => r + 1)}
            disabled={feed.loading}
            variant="contained"
            startIcon={<ArrowSync />}
          >
            {t("dashboard:node.refresh")}
          </SecondaryButton>
          <DenseSelect value={group || " "} onChange={(e) => setGroup((e.target.value as string).trim())}>
            {groups.map((g) => (
              <SquareMenuItem key={g || " "} value={g || " "}>
                <ListItemText
                  primary={t(`activity.group_${g || "all"}`)}
                  slotProps={{ primary: { variant: "body2" } }}
                />
              </SquareMenuItem>
            ))}
          </DenseSelect>
          {account && (
            <Chip
              size="small"
              label={accountName(account)}
              onDelete={() => setAccount(undefined)}
            />
          )}
        </Stack>
        <Stack direction="row" spacing={1} sx={{ mb: 2, alignItems: "center", flexWrap: "wrap" }}>
          <Typography variant="body2">{t("activity.retention")}</Typography>
          <DenseFilledTextField
            type="number"
            value={retention}
            onChange={(e) => setRetention(e.target.value)}
            slotProps={{ htmlInput: { min: 0, max: 3650 } }}
            sx={{ width: 100 }}
          />
          <Typography variant="body2" color="text.secondary">
            {t("activity.retentionDays")}
          </Typography>
          <LoadingButton
            variant="contained"
            size="small"
            loading={saving}
            disabled={retention === savedRetention || retention === ""}
            onClick={saveRetention}
          >
            <span>{t("fileManager.save")}</span>
          </LoadingButton>
        </Stack>
        <TableContainer component={StyledTableContainerPaper}>
          <Table size="small" stickyHeader sx={{ width: "100%", tableLayout: "fixed" }}>
            <TableHead>
              <TableRow>
                <NoWrapTableCell width={150}>{t("activity.when")}</NoWrapTableCell>
                <NoWrapTableCell width={140}>{t("activity.who")}</NoWrapTableCell>
                <NoWrapTableCell width={420}>{t("activity.what")}</NoWrapTableCell>
                <NoWrapTableCell width={140}>{t("activity.owner")}</NoWrapTableCell>
                <NoWrapTableCell width={160}>{t("activity.device")}</NoWrapTableCell>
                <NoWrapTableCell width={140}>{t("activity.address")}</NoWrapTableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {feed.entries.map((entry) => (
                <TableRow key={entry.id} hover>
                  <TableCell>
                    <TimeBadge datetime={entry.created_at} variant="body2" />
                  </TableCell>
                  <TableCell>{actorCell(entry)}</TableCell>
                  <TableCell
                    sx={{
                      wordBreak: "break-word",
                      color: isFailedSignIn(entry) ? theme.palette.error.main : undefined,
                    }}
                  >
                    {activityText(t, entry)}
                  </TableCell>
                  <TableCell>{accountCell(entry.owner)}</TableCell>
                  <TableCell sx={{ wordBreak: "break-word" }} title={entry.user_agent}>
                    {deviceName(t, entry)}
                  </TableCell>
                  <TableCell sx={{ wordBreak: "break-all" }}>{entry.ip}</TableCell>
                </TableRow>
              ))}
              {!feed.loading && feed.entries.length === 0 && (
                <TableRow>
                  <TableCell colSpan={6} align="center">
                    <Typography variant="body2" color="text.secondary">
                      {t("activity.empty")}
                    </Typography>
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </TableContainer>
        {feed.hasMore && (
          <Box sx={{ mt: 2 }}>
            <SecondaryButton variant="contained" disabled={feed.loading} onClick={feed.loadMore}>
              {t("activity.loadMore")}
            </SecondaryButton>
          </Box>
        )}
      </Container>
    </AdminPage>
  );
};

export default ActivityLog;
