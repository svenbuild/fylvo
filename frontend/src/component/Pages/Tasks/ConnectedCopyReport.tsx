import { Alert, Box, Button, Stack, Typography } from "@mui/material";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { sendCancelConnectedCopy } from "../../../api/api.ts";
import { TaskResponse, TaskStatus } from "../../../api/workflow.ts";
import { useAppDispatch } from "../../../redux/hooks.ts";
import { confirmOperation } from "../../../redux/thunks/dialog.ts";

// Fylvo: the report of a copy between Fylvo and connected storages, and the way to stop a
// copy that runs. Every entry that was not copied is named with its reason, so nobody takes
// a file that was passed over for one that was copied.
const ConnectedCopyReport = ({ task }: { task: TaskResponse }) => {
  const { t } = useTranslation();
  const dispatch = useAppDispatch();
  const [stopping, setStopping] = useState(false);
  const report = task.summary?.props.connected_copy;
  // Also a copy that waits for a place, or to be resumed.
  const running =
    task.status == TaskStatus.queued || task.status == TaskStatus.processing || task.status == TaskStatus.suspending;

  const stop = () => {
    dispatch(confirmOperation(t("setting.connectedCopyStopConfirm"))).then(() => {
      setStopping(true);
      dispatch(sendCancelConnectedCopy(task.id)).finally(() => setStopping(false));
    });
  };

  return (
    <Stack spacing={1}>
      {running && (
        <Box>
          <Button variant="outlined" color="error" size="small" disabled={stopping} onClick={stop}>
            {t("setting.connectedCopyStop")}
          </Button>
        </Box>
      )}
      {report && !running && (
        <Typography variant="body2">
          {t("setting.connectedCopyCounts", {
            copied: report.copied,
            skipped: report.skipped,
            failed: report.failed,
          })}
        </Typography>
      )}
      {report?.stopped && (
        <Alert severity={report.stopped == "full" ? "error" : "warning"}>
          {t(report.stopped == "full" ? "setting.connectedCopyEndedFull" : "setting.connectedCopyEndedStopped")}
        </Alert>
      )}
      {!!report?.notes?.length && (
        <Alert severity="info" icon={false}>
          <Stack spacing={0.5}>
            {report.notes.map((note, i) => (
              <Typography key={i} variant="body2" sx={{ wordBreak: "break-all" }}>
                <b>{t(`setting.connectedCopyReason.${note.reason}`, { defaultValue: note.reason })}</b>
                {": "}
                {decodeURIComponent(note.path.replace(/^cloudreve:\/\/[^/]*/, ""))}
              </Typography>
            ))}
            {!!report.notes_left && (
              <Typography variant="body2">{t("setting.connectedCopyMore", { num: report.notes_left })}</Typography>
            )}
          </Stack>
        </Alert>
      )}
    </Stack>
  );
};

export default ConnectedCopyReport;
