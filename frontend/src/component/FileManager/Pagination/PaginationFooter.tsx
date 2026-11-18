import { Box, Pagination, Slide, styled, useMediaQuery, useTheme } from "@mui/material";
import { forwardRef, useContext } from "react";
import { useAppDispatch, useAppSelector } from "../../../redux/hooks.ts";
import { changePage } from "../../../redux/thunks/filemanager.ts";
import { selectedSurface } from "../../../fylvoTheme.ts";
import { RadiusFrame } from "../../Frame/RadiusFrame.tsx";
import { MinPageSize } from "../TopBar/ViewOptionPopover.tsx";
import PaginationItem from "./PaginationItem.tsx";

import { PaginationResults } from "../../../api/explorer.ts";
import { FmIndexContext } from "../FmIndexContext.tsx";

const PaginationFrame = styled(RadiusFrame)(({ theme }) => ({
  padding: theme.spacing(0.5),
}));

export interface PaginationState {
  currentPage: number;
  totalPages: number;
  usePagination: boolean;
  moreItems: boolean;
  useEndlessLoading: boolean;
  nextToken?: string;
}

export const usePaginationState = (fmIndex: number) => {
  const pagination = useAppSelector((state) => state.fileManager[fmIndex].list?.pagination);
  return getPaginationState(pagination);
};

export const getPaginationState = (pagination?: PaginationResults) => {
  const totalItems = pagination?.total_items;
  const page = pagination?.page;
  const pageSize = pagination?.page_size;

  const currentPage = (page ?? 0) + 1;
  const totalPages = Math.ceil((totalItems ?? 1) / (pageSize && pageSize > 0 ? pageSize : MinPageSize));
  const usePagination = totalPages > 1;
  return {
    currentPage,
    totalPages,
    usePagination,
    useEndlessLoading: !usePagination,
    moreItems: pagination?.next_token || (usePagination && currentPage < totalPages),
    nextToken: pagination?.next_token,
  } as PaginationState;
};

const PaginationFooter = forwardRef((_props, ref) => {
  const theme = useTheme();
  const isMobile = useMediaQuery(theme.breakpoints.down("sm"));
  const dispatch = useAppDispatch();
  const fmIndex = useContext(FmIndexContext);
  const paginationState = usePaginationState(fmIndex);
  const onPageChange = (_event: unknown, page: number) => {
    dispatch(changePage(fmIndex, page - 1));
  };

  return (
    <Slide direction={"up"} unmountOnExit in={paginationState.usePagination}>
      <Box ref={ref} sx={{ display: "flex", px: isMobile ? 1.5 : 0, py: 1 }}>
        <PaginationFrame withBorder>
          <Pagination
            renderItem={(item) => <PaginationItem {...item} />}
            shape="rounded"
            sx={{
              "& .MuiPaginationItem-root": {
                minWidth: 28,
                height: 28,
                "@media (max-width: 599.95px), (pointer: coarse)": { minWidth: 40, height: 40 },
                "&.Mui-selected, &.Mui-selected:hover": { bgcolor: selectedSurface },
                "&.Mui-focusVisible": { outline: "1px solid", outlineColor: "primary.main" },
              },
            }}
            count={paginationState.totalPages}
            page={paginationState.currentPage}
            onChange={onPageChange}
          />
        </PaginationFrame>
      </Box>
    </Slide>
  );
});

export default PaginationFooter;
