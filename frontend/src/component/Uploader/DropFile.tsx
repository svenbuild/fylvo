import React from "react";
import Backdrop from "@mui/material/Backdrop";
import { Box, Typography } from "@mui/material";
import { useTranslation } from "react-i18next";
import CloudArrowIUp from "../Icons/CloudArrowIUp.tsx";

export function DropFileBackground({ open }: { open: boolean }) {
  const { t } = useTranslation();
  return (
    <Backdrop
      sx={{
        zIndex: (theme) => theme.zIndex.drawer + 1,
        color: "text.primary",
      }}
      open={open}
    >
      <Box
        sx={{
          display: "flex",
          alignItems: "center",
          gap: 1,
          px: "16px",
          py: "12px",
          bgcolor: "background.paper",
          border: 1,
          borderColor: "divider",
          borderRadius: 1,
        }}
      >
        <CloudArrowIUp color="action" />
        <Typography variant="subtitle2">{t("uploader.dropFileHere")}</Typography>
      </Box>
    </Backdrop>
  );
}
