import React from "react";
import { Box, Typography } from "@mui/material";
import PackageOpen from "../Icons/PackageOpen.tsx";

export interface NothingProps {
  primary: string;
  secondary?: string;
  top?: number;
  size?: number;
}

export default function Nothing({ primary, secondary, top = 20, size = 1 }: NothingProps) {
  return (
    <Box
      sx={{
        margin: `${16 * size}px auto`,
        paddingTop: `${top}px`,
        color: "text.secondary",
        textAlign: "center",
      }}
    >
      <PackageOpen
        sx={{
          fontSize: 32 * size,
          mb: "4px",
        }}
      />
      <Typography
        variant="subtitle2"
        color="text.primary"
      >
        {primary}
      </Typography>
      {secondary && (
        <Typography variant="caption" color="text.secondary">
          {secondary}
        </Typography>
      )}
    </Box>
  );
}
