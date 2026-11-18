import React, { useCallback, useContext, useMemo, useState } from "react";

import { Box, Fade, IconButton, Skeleton, styled, Tooltip, Typography } from "@mui/material";
import { TreeItem, treeItemClasses, TreeItemContentProps, TreeItemProps, useTreeItem } from "@mui/x-tree-view";
import clsx from "clsx";
import { useTranslation } from "react-i18next";
import { hoverSurface } from "../../../fylvoTheme.ts";
import { FileResponse } from "../../../api/explorer.ts";
import { useAppDispatch } from "../../../redux/hooks.ts";
import { openFileContextMenu } from "../../../redux/thunks/file.ts";
import { loadChild, navigateToPath } from "../../../redux/thunks/filemanager.ts";
import { unPinFromSidebar } from "../../../redux/thunks/settings.ts";
import { mergeRefs } from "../../../util";
import FacebookCircularProgress from "../../Common/CircularProgress.tsx";
import NavIconTransition from "../../Frame/NavBar/NavIconTransition.tsx";
import { SideNavItemBase } from "../../Frame/NavBar/SideNavItem.tsx";
import CaretDown from "../../Icons/CaretDown.tsx";
import Dismiss from "../../Icons/Dismiss.tsx";
import { useFileDrag } from "../Dnd/DndWrappedFile.tsx";
import FileIcon from "../Explorer/FileIcon.tsx";
import { FmIndexContext } from "../FmIndexContext.tsx";
import { StartIcon } from "../TopBar/BreadcrumbButton.tsx";
import { pinedPrefix } from "./TreeFiles.tsx";

const CustomContentRoot = styled(SideNavItemBase)<{
  isDragging?: boolean;
  isDropOver?: boolean;
}>(({ theme, isDragging, isDropOver }) => ({
  "&&": {
    paddingBlock: 0,
    paddingRight: 6,
    "&:hover, &.Mui-focused": {
      backgroundColor: hoverSurface(theme),
    },
  },
  "&& .MuiTreeItem-iconContainer": {
    width: 16,
    flexShrink: 0,
    marginRight: 4,
    color: theme.palette.text.secondary,
  },
  opacity: isDragging ? 0.5 : 1,
  boxShadow: isDropOver ? `inset 0 0 0 2px ${theme.palette.primary.light}` : "none",
}));

const StyledTreeItemRoot = styled(TreeItem)(({ theme }) => ({
  [`&:focus-visible > .${treeItemClasses.content}`]: {
    outline: `1px solid ${theme.palette.primary.main}`,
    outlineOffset: -1,
  },
  [`& .${treeItemClasses.group}`]: {
    marginLeft: 0,
    [`& .${treeItemClasses.content}`]: {
      marginLeft: 0,
    },
  },
})) as unknown as typeof TreeItem;

export const CaretDownIcon = styled(CaretDown)<{ expanded: boolean }>(({ theme, expanded }) => ({
  fontSize: "12px!important",
  transform: `rotate(${expanded ? 0 : -90}deg)`,
  transition: theme.transitions.create("transform", {
    duration: theme.transitions.duration.shortest,
    easing: theme.transitions.easing.easeInOut,
  }),
}));

export interface CustomContentProps extends TreeItemContentProps {
  parent?: string;
  level?: number;
  notLoaded?: boolean;
  file?: FileResponse;
  fileIcon?: StartIcon;
  loading?: boolean;
  pinned?: boolean;
  canDrop?: boolean;
}

export interface TreeFileProps extends TreeItemProps {
  parent?: string;
  level?: number;
  notLoaded?: boolean;
  file?: FileResponse;
  fileIcon?: StartIcon;
  loading?: boolean;
  pinned?: boolean;
  canDrop?: boolean;
}

const SmallIconButton = styled(IconButton)(() => ({
  fontSize: "0.8rem",
}));

interface UnpinButton {
  show: boolean;
  uri: string;
}
const UnpinButton = (props: UnpinButton) => {
  const { t } = useTranslation();
  const dispatch = useAppDispatch();
  const [loading, setLoading] = useState(false);
  const onClick = useCallback(
    async (e: React.MouseEvent<HTMLButtonElement>) => {
      e.stopPropagation();
      setLoading(true);
      try {
        await dispatch(unPinFromSidebar(props.uri));
      } catch (e) {
        setLoading(false);
      }
    },
    [setLoading],
  );

  return (
    <Fade in={props.show} unmountOnExit>
      <Tooltip title={t("application:fileManager.unpin")}>
        <SmallIconButton disabled={loading} onMouseDown={(e) => e.stopPropagation()} onClick={onClick} size="small">
          <Dismiss fontSize={"inherit"} />
        </SmallIconButton>
      </Tooltip>
    </Fade>
  );
};

const CustomContent = React.memo(
  React.forwardRef(function CustomContent(props: CustomContentProps, ref) {
    const dispatch = useAppDispatch();
    const fmIndex = useContext(FmIndexContext);
    const [loading, setLoading] = useState(false);
    const [showDelete, setShowDelete] = useState(false);
    const { classes, className, label, nodeId, icon: iconProp, expansionIcon, displayIcon, file, fileIcon } = props;

    const { disabled, expanded, selected, focused, handleExpansion, handleSelection, preventSelection } =
      useTreeItem(nodeId);

    const uri = useMemo(() => {
      // Trim 'pinedPrefix' if exist in prefix
      if (nodeId.startsWith(pinedPrefix)) {
        return nodeId.substring(pinedPrefix.length);
      }

      return nodeId;
    }, [nodeId]);

    const icon = iconProp || expansionIcon || displayIcon;

    const handleMouseDown = (event: React.MouseEvent<HTMLDivElement, MouseEvent>) => {
      preventSelection(event);
    };

    const handleExpansionClick = useCallback(
      async (event: React.MouseEvent<HTMLDivElement, MouseEvent>) => {
        event.stopPropagation();
        let timeOutID: NodeJS.Timeout | undefined;
        handleExpansion(event);
        if (!expanded) {
          try {
            await dispatch(loadChild(fmIndex, uri, () => (timeOutID = setTimeout(() => setLoading(true), 300))));
          } finally {
            if (timeOutID) {
              clearTimeout(timeOutID);
            }
            setLoading(false);
          }
        }
      },
      [handleExpansion, setLoading, dispatch, uri],
    );

    const handleSelectionClick = useCallback(
      (event: React.MouseEvent<HTMLDivElement, MouseEvent>) => {
        event.stopPropagation();
        handleSelection(event);
        dispatch(navigateToPath(fmIndex, uri, file));
      },
      [dispatch, handleSelection, fmIndex, uri],
    );

    const FileItemIcon = useMemo(() => {
      if (props.loading) {
        return <Skeleton variant={"rounded"} width={16} height={16} />;
      }
      if (fileIcon && fileIcon.Icons) {
        return <NavIconTransition iconProps={{ fontSize: "small" }} fileIcon={fileIcon.Icons} active={selected} />;
      }
      if (fileIcon && fileIcon.Element) {
        return <fileIcon.Element sx={{ height: 16, width: 16 }} />;
      }
      return (
        <FileIcon
          variant={"small"}
          file={file}
          sx={{ p: 0, display: "flex" }}
          fontSize={"small"}
          iconProps={{ fontSize: "small", sx: { display: "block", color: "text.secondary" } }}
          notLoaded={props.notLoaded}
        />
      );
    }, [file, fileIcon, selected, props.notLoaded, props.loading]);

    const onContextMenu = useCallback(
      (e: React.MouseEvent<HTMLElement>) => {
        if (!file || file.name === "") {
          return;
        }
        dispatch(openFileContextMenu(fmIndex, file, true, e));
      },
      [file, dispatch, fmIndex],
    );

    const fileName = useMemo(
      () => (
        <Tooltip title={label} disableInteractive>
          <Typography
            onClick={handleSelectionClick}
            component="div"
            noWrap
            sx={{
              flexGrow: 1,
              textAlign: "left",
            }}
            variant={"body2"}
          >
            {!props.loading && label}
            {props.loading && <Skeleton variant={"text"} width={75} />}
          </Typography>
        </Tooltip>
      ),
      [label, handleSelectionClick, props.loading],
    );

    const onMouseEnter = useCallback(() => {
      if (props.pinned) setShowDelete(true);
    }, [setShowDelete, props.pinned]);

    const onMouseLeave = useCallback(() => {
      if (props.pinned) setShowDelete(false);
    }, [setShowDelete, props.pinned]);

    const [drag, drop, isOver, isDragging] = useFileDrag({
      file,
      dropUri: props.canDrop ? nodeId : undefined,
    });

    const mergedRef = useCallback(
      (val: any) => {
        mergeRefs(ref as React.Ref<HTMLDivElement>, drop, drag)(val);
      },
      [ref, drop, drag],
    );

    return (
      // eslint-disable-next-line jsx-a11y/no-static-element-interactions
      <CustomContentRoot
        onContextMenu={onContextMenu}
        onMouseEnter={onMouseEnter}
        onMouseLeave={onMouseLeave}
        isDragging={isDragging}
        isDropOver={isOver && !isDragging}
        className={clsx(className, classes.root, {
          [classes.expanded]: expanded,
          [classes.selected]: selected,
          [classes.focused]: focused,
          [classes.disabled]: disabled,
        })}
        active={selected}
        sx={{
          "&&": { paddingLeft: `${4 + (props.level ?? 0) * 10}px` },
        }}
        onClick={handleSelectionClick}
        ref={mergedRef}
      >
        {/* eslint-disable-next-line jsx-a11y/click-events-have-key-events,jsx-a11y/no-static-element-interactions */}
        <div onClick={handleExpansionClick} className={classes.iconContainer}>
          {icon && !loading && <CaretDownIcon expanded={expanded} />}
          {icon && loading && <FacebookCircularProgress size={12} />}
        </div>
        <Box sx={{ width: 16, mr: 1, flexShrink: 0, display: "flex", color: "text.secondary" }}>{FileItemIcon}</Box>
        {fileName}
        <UnpinButton show={showDelete} uri={uri} />
      </CustomContentRoot>
    );
  }),
);

const TreeFile = React.memo(
  React.forwardRef(function CustomTreeItem(props: TreeFileProps, ref: React.Ref<HTMLLIElement>) {
    const contentProps = useMemo(() => {
      const { level, file, notLoaded, fileIcon, loading, pinned, canDrop } = props;
      return { level, file, notLoaded, fileIcon, loading, pinned, canDrop };
    }, [props.level, props.file, props.notLoaded, props.fileIcon, props.loading, props.canDrop, props.pinned]);
    return (
      <StyledTreeItemRoot
        ContentComponent={CustomContent}
        // @ts-ignore
        ContentProps={contentProps}
        {...props}
        ref={ref}
      />
    );
  }),
);

export default TreeFile;
