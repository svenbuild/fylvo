import { Box, Stack, Typography } from "@mui/material";
import { useEffect, useState } from "react";
import { FileResponse, FileType, Metadata } from "../../../api/explorer.ts";
import { useAppDispatch } from "../../../redux/hooks.ts";
import { loadFileThumb } from "../../../redux/thunks/file.ts";
import { FileManagerIndex } from "../FileManager.tsx";
import BasicInfo from "./BasicInfo.tsx";
import CustomProps from "./CustomProps/CustomProps.tsx";
import Data from "./Data.tsx";
import MediaInfo from "./MediaInfo.tsx";
import Tags from "./Tags.tsx";
import { DisplayOption } from "../ContextMenu/useActionDisplayOpt.ts";
import { inConnectedStorage } from "../../../api/connected.ts";

export interface DetailsProps {
  inPhotoViewer?: boolean;
  target: FileResponse;
  setTarget: (target: FileResponse | undefined | null) => void;
  targetDisplayOptions?: DisplayOption;
}

const InfoBlock = ({ title, children }: { title: string; children: React.ReactNode }) => {
  return (
    <Box>
      <Typography variant={"body2"}>{title}</Typography>
      <Typography variant={"body2"} color={"text.secondary"}>
        {children}
      </Typography>
    </Box>
  );
};

const Details = ({ target, inPhotoViewer, setTarget, targetDisplayOptions }: DetailsProps) => {
  const dispatch = useAppDispatch();
  const [thumbSrc, setThumbSrc] = useState<string | null>(null);
  useEffect(() => {
    if (target.type == FileType.file && (!target.metadata || target.metadata[Metadata.thumbDisabled] === undefined)) {
      dispatch(loadFileThumb(FileManagerIndex.main, target)).then((src) => {
        setThumbSrc(src);
      });
    }

    setThumbSrc(null);
  }, [target]);

  return (
    <Stack
      spacing={1}
      sx={{
        "& .MuiTypography-subtitle1": {
          fontSize: 12,
          fontWeight: 600,
          color: "text.secondary",
          borderTop: 1,
          borderColor: "divider",
          pt: 1,
        },
        "& > .MuiTypography-subtitle1:first-child": { borderTop: 0, pt: 0 },
      }}
    >
      {thumbSrc && !inPhotoViewer && (
        <Box
          onError={() => {
            setThumbSrc(null);
          }}
          src={thumbSrc}
          sx={{
            borderRadius: "8px",
            border: 1,
            borderColor: "divider",
            width: "100%",
            maxHeight: 144,
            objectFit: "contain",
          }}
          component={"img"}
        />
      )}
      <MediaInfo target={target} />
      {/* Fylvo: a connected storage keeps no properties of this server. */}
      {!inConnectedStorage(target.path) && (
        <CustomProps file={target} setTarget={setTarget} targetDisplayOptions={targetDisplayOptions} />
      )}
      <BasicInfo target={target} />
      <Tags target={target} />
      <Data target={target} />
    </Stack>
  );
};

export default Details;
