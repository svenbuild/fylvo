import { useEffect, useState } from "react";
import { Box, Typography, Chip, Stack } from "@mui/material";
import { useTranslation } from "react-i18next";
import { getVersion } from "@tauri-apps/api/app";
import logo from "../../assets/fylvo.svg";

function isPreviewVersion(version: string): boolean {
  if (version.startsWith("0.")) return true;
  if (version.includes("-")) return true;
  return false;
}

export default function AboutSection() {
  const { t } = useTranslation();
  const [version, setVersion] = useState<string>("");

  useEffect(() => {
    getVersion().then(setVersion);
  }, []);

  const isPreview = version && isPreviewVersion(version);

  return (
    <Box>
      <Typography variant="h5" fontWeight={500} gutterBottom>
        {t("settings.about")}
      </Typography>

      <Stack direction="row" alignItems="center" spacing={2} sx={{ mb: 3 }}>
        <Box
          component="img"
          src={logo}
          alt="Fylvo"
          sx={{ width: 48, height: 48 }}
        />
        <Box>
          <Typography variant="h6" fontWeight={500}>
            Fylvo Desktop
          </Typography>
          <Stack direction="row" alignItems="center" spacing={1}>
            <Typography variant="body2" color="text.secondary">
              {version ? `v${version}` : "..."}
            </Typography>
            {isPreview && (
              <Chip
                label={t("about.preview")}
                size="small"
                sx={{ height: 20, fontSize: "0.7rem" }}
              />
            )}
          </Stack>
        </Box>
      </Stack>

      <Typography variant="body2" color="text.secondary">
        Based on Cloudreve Desktop, MIT License, Copyright (c) 2025 Cloudreve.
      </Typography>
    </Box>
  );
}
