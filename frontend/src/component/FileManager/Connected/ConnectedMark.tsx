import { Alert, Box, SxProps, Theme, Tooltip } from "@mui/material";
import { useContext } from "react";
import { useTranslation } from "react-i18next";
import { FileResponse, Metadata } from "../../../api/explorer.ts";
import { inConnectedStorage } from "../../../api/connected.ts";
import LinkOutlined from "../../Icons/LinkOutlined.tsx";
import WarningOutlined from "../../Icons/WarningOutlined.tsx";
import { useAppSelector } from "../../../redux/hooks.ts";
import { FmIndexContext } from "../FmIndexContext.tsx";

// Fylvo: a small mark next to the name of a row of a connected storage that is a shortcut at
// the provider, or that cannot be used here because its name exists more than once.
const ConnectedMark = ({ file }: { file: FileResponse }) => {
  const { t } = useTranslation("application");
  const metadata = file.metadata;
  if (metadata?.[Metadata.connected_unsupported] !== undefined) {
    return (
      <Tooltip title={t("connected.rowDuplicate")}>
        <WarningOutlined sx={{ fontSize: 14, flexShrink: 0, color: "warning.main" }} />
      </Tooltip>
    );
  }
  if (metadata?.[Metadata.connected_link] !== undefined) {
    return (
      <Tooltip title={t("connected.rowLink")}>
        <LinkOutlined sx={{ fontSize: 14, flexShrink: 0, color: "text.secondary" }} />
      </Tooltip>
    );
  }
  return null;
};

export default ConnectedMark;

// Whether the size of a row is known; rows of a connected storage may not have one, and a
// row that cannot be used has none.
export const sizeKnown = (file: FileResponse) =>
  file.metadata?.[Metadata.connected_size_unknown] === undefined &&
  file.metadata?.[Metadata.connected_unsupported] === undefined;

// Whether a time is known: the server sends the zero time (year 1) when the provider names
// none, for example for the root of a connected storage.
export const timeKnown = (time?: string) => !!time && !time.startsWith("0001-01-01");

// A row that is listed but cannot be used; shown dimmed.
export const rowUnsupported = (file: FileResponse) => file.metadata?.[Metadata.connected_unsupported] !== undefined;

// A note below a listing of a connected storage when entries could not be shown, because
// their names cannot be a name here (for example a name with "/" at Google Drive).
export const ConnectedOmitted = ({ sx }: { sx?: SxProps<Theme> }) => {
  const { t } = useTranslation("application");
  const fmIndex = useContext(FmIndexContext);
  const omitted = useAppSelector((state) => state.fileManager[fmIndex].list?.connected?.omitted ?? 0);
  if (omitted <= 0) {
    return null;
  }
  return (
    <Box sx={sx}>
      <Alert severity="info">{t("connected.omitted", { count: omitted })}</Alert>
    </Box>
  );
};

// A note above a search result of a connected storage that did not see everything, and how many
// shortcuts it showed without searching them.
export const ConnectedIncomplete = ({ sx }: { sx?: SxProps<Theme> }) => {
  const { t } = useTranslation("application");
  const fmIndex = useContext(FmIndexContext);
  const info = useAppSelector((state) => state.fileManager[fmIndex].list?.connected);
  const links = info?.links ?? 0;
  const incomplete = !!info?.incomplete;
  if (!incomplete && links <= 0) {
    return null;
  }
  return (
    <Box sx={sx}>
      <Alert severity="info">
        {incomplete && t("connected.searchIncomplete")}
        {incomplete && links > 0 && " "}
        {links > 0 && t("connected.searchLinks", { count: links })}
      </Alert>
    </Box>
  );
};

// The name of "delete": in a connected storage it moves to the provider's trash.
export const deleteLabel = (targets: FileResponse[]) =>
  targets.some((f) => inConnectedStorage(f.path))
    ? "application:connected.toProviderTrash"
    : "application:fileManager.delete";
