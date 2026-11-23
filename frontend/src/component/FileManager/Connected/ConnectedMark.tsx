import { Alert, Box, SxProps, Theme, Tooltip } from "@mui/material";
import { useContext } from "react";
import { useTranslation } from "react-i18next";
import { FileResponse, Metadata } from "../../../api/explorer.ts";
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

// Whether the size of a row is known; rows of a connected storage may not have one.
export const sizeKnown = (file: FileResponse) => file.metadata?.[Metadata.connected_size_unknown] === undefined;

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
