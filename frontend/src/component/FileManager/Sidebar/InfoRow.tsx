import { Box, Typography } from "@mui/material";
import React from "react";

export interface InfoRowProps {
  title: string;
  content: React.ReactNode | string;
}

const InfoRow = ({ title, content }: InfoRowProps) => {
  return (
    <Box sx={{ display: "grid", gridTemplateColumns: "minmax(0, 2fr) minmax(0, 3fr)", gap: 1.5, alignItems: "baseline" }}>
      <Typography component="span" variant="caption" color="text.secondary" sx={{ overflowWrap: "anywhere" }}>
        {title}
      </Typography>
      <Box sx={{ typography: "body2", color: "text.primary", minWidth: 0, overflowWrap: "anywhere" }}>
        {content}
      </Box>
    </Box>
  );
};

export default InfoRow;
