import { Box, Stack, styled, Typography } from "@mui/material";
import React, { useContext, useMemo } from "react";
import { useTranslation } from "react-i18next";
import { FileResponse, FileType } from "../../../../api/explorer.ts";
import { useAppSelector } from "../../../../redux/hooks.ts";
import DndWrappedFile from "../../Dnd/DndWrappedFile.tsx";
import { FmIndexContext } from "../../FmIndexContext.tsx";
import GridFile from "./GridFile.tsx";
import { CrUriPrefix } from "../../../../util/uri.ts";

export interface GridViewProps {
  [key: string]: any;
}

export interface FmFile extends FileResponse {
  id: string;
  first?: boolean;
  placeholder?: boolean;
}

interface listComponents {
  Folders?: JSX.Element[];
  Files: JSX.Element[];
}

const AutoFillGrid = styled(Box)(({ theme }) => ({
  [theme.breakpoints.down("md")]: {
    gridTemplateColumns: "repeat(auto-fill, minmax(min(100%, 144px), 1fr))",
  },
  [theme.breakpoints.up("md")]: {
    gridTemplateColumns: "repeat(auto-fill, minmax(168px, 1fr))",
  },
  gap: 8,
  display: "grid",
}));

export const loadingPlaceHolderNumb = 3;

const GridView = React.forwardRef(({ ...rest }: GridViewProps, ref) => {
  const { t } = useTranslation("application");
  const fmIndex = useContext(FmIndexContext);
  const files = useAppSelector((state) => state.fileManager[fmIndex].list?.files);
  const mixedType = useAppSelector((state) => state.fileManager[fmIndex].list?.mixed_type);
  const pagination = useAppSelector((state) => state.fileManager[fmIndex].list?.pagination);
  const showThumb = useAppSelector((state) => state.fileManager[fmIndex].showThumb);
  const search_params = useAppSelector((state) => state.fileManager[fmIndex]?.search_params);
  const list = useMemo(() => {
    const list: listComponents = {
      Files: [],
    };
    if (files) {
      files.forEach((file, index) => {
        if (file.type === FileType.folder && !mixedType) {
          if (!list.Folders) {
            list.Folders = [];
          }
          list.Folders.push(
            <Box key={file.id} sx={{ minWidth: 0 }}>
              <DndWrappedFile
                component={GridFile}
                search={search_params}
                index={index}
                showThumb={mixedType}
                file={file}
              />
            </Box>,
          );
        } else {
          list.Files.push(
            <Box key={file.id} sx={{ minWidth: 0 }}>
              <DndWrappedFile
                component={GridFile}
                search={search_params}
                index={index}
                showThumb={showThumb}
                file={file}
              />
            </Box>,
          );
        }
      });

      // Add loading placeholder if there is next page
      if (pagination && pagination.next_token) {
        for (let i = 0; i < loadingPlaceHolderNumb; i++) {
          const id = `loadingPlaceholder-${pagination.next_token}-${i}`;
          const loadingPlaceholder = (
            <Box key={id} sx={{ minWidth: 0 }}>
              <DndWrappedFile
                component={GridFile}
                showThumb={list.Files.length > 0 ? showThumb : mixedType}
                file={{
                  ...files[0],
                  path: `${CrUriPrefix}${id}`,
                  id: id,
                  first: i == 0,
                  placeholder: true,
                }}
              />
            </Box>
          );
          const _ =
            list.Files.length > 0 ? list.Files.push(loadingPlaceholder) : list.Folders?.push(loadingPlaceholder);
        }
      }
    }
    return list;
  }, [files, mixedType, pagination, showThumb]);
  return (
    <Box
      ref={ref}
      {...rest}
      sx={{
        p: { xs: 1.5, sm: 2 },
      }}
    >
      <Stack spacing={2}>
        {list.Folders && list.Folders.length > 0 && (
          <Box>
            <Typography component="h2" variant="caption" color="text.secondary" fontWeight={600} sx={{ mb: 1 }}>
              {t("fileManager.folders")}
            </Typography>
            <AutoFillGrid>
              {list.Folders.map((f) => f)}
            </AutoFillGrid>
          </Box>
        )}
        {list.Files.length > 0 && (
          <Box>
            {!mixedType && (
              <Typography component="h2" variant="caption" color="text.secondary" fontWeight={600} sx={{ mb: 1 }}>
                {t("fileManager.files")}
              </Typography>
            )}
            <AutoFillGrid>
              {list.Files.map((f) => f)}
            </AutoFillGrid>
          </Box>
        )}
      </Stack>
    </Box>
  );
});

export default GridView;
