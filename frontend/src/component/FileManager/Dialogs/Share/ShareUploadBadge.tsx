import { Tooltip, Typography } from "@mui/material";
import { useTranslation } from "react-i18next";
import { Share } from "../../../../api/explorer.ts";
import { sizeToString } from "../../../../util";
import Upload from "../../../Icons/Upload.tsx";

// Fylvo: marks a link that accepts uploads and, for the owner, how much it has received so far.
const ShareUploadBadge = ({ share }: { share: Share }) => {
  const { t } = useTranslation();
  // uploaded and upload_limit are only sent to the owner.
  let received = share.uploaded != undefined || share.upload_limit ? sizeToString(share.uploaded ?? 0) : undefined;
  if (received && share.upload_limit) {
    received += " / " + sizeToString(share.upload_limit);
  }
  return (
    <Tooltip title={t(`application:modals.${share.hide_content ? "uploadOnly" : "viewAndUpload"}`)}>
      <Typography
        variant="caption"
        color="text.secondary"
        sx={{ display: "inline-flex", alignItems: "center", gap: 0.5, flexShrink: 0 }}
      >
        <Upload sx={{ fontSize: 14 }} />
        {t("application:share.uploadBadge")}
        {received && `: ${received}`}
      </Typography>
    </Tooltip>
  );
};

export default ShareUploadBadge;
