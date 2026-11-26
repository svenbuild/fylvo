import DeleteConfirmation from "./DeleteConfirmation.tsx";
import AggregatedErrorDetail from "../../Dialogs/AggregatedErrorDetail.tsx";
import LockConflictDetails from "./LockConflictDetails.tsx";
import Rename from "./Rename.tsx";
import PathSelection from "./PathSelection.tsx";
import Tags from "./Tags.tsx";
import ChangeIcon from "./ChangeIcon.tsx";
import ShareDialog from "./Share/ShareDialog.tsx";
import VersionControl from "./VersionControl.tsx";
import ManageShares from "./Share/ManageShares.tsx";
import StaleVersionConfirm from "./StaleVersionConfirm.tsx";
import SaveAs from "./SaveAs.tsx";
import Photopea from "../../Viewers/Photopea/Photopea.tsx";
import OpenWith from "./OpenWith.tsx";
import Wopi from "../../Viewers/Wopi.tsx";
import CraftViewer from "../../Viewers/Craft/CraftViewer.tsx";
import CodeViewer from "../../Viewers/CodeViewer/CodeViewer.tsx";
import DrawIOViewer from "../../Viewers/DrawIO/DrawIOViewer.tsx";
import MarkdownViewer from "../../Viewers/MarkdownEditor/MarkdownViewer.tsx";
import VideoViewer from "../../Viewers/Video/VideoViewer.tsx";
import PdfViewer from "../../Viewers/PdfViewer.tsx";
import CustomViewer from "../../Viewers/CustomViewer.tsx";
import EpubViewer from "../../Viewers/EpubViewer/EpubViewer.tsx";
import CreateNew from "./CreateNew.tsx";
import { useAppSelector } from "../../../redux/hooks.ts";
import React, { lazy, Suspense } from "react";
import DirectLinks from "./DirectLinks/DirectLinks.tsx";

// Fylvo: dialogs that only open on demand load their code then, not with the file manager.
const CreateArchive = lazy(() => import("./CreateArchive.tsx"));
const ExtractArchive = lazy(() => import("./ExtractArchive.tsx"));
const CreateRemoteDownload = lazy(() => import("./CreateRemoteDownload.tsx"));
const AdvanceSearch = lazy(() => import("../Search/AdvanceSearch/AdvanceSearch.tsx"));
const ColumnSetting = lazy(() => import("../Explorer/ListView/ColumnSetting.tsx"));
const ExcalidrawViewer = lazy(() => import("../../Viewers/Excalidraw/ExcalidrawViewer.tsx"));
const DirectLinksControl = lazy(() => import("./DirectLinksControl.tsx"));
const ArchivePreview = lazy(() => import("../../Viewers/ArchivePreview/ArchivePreview.tsx"));
const DesktopMountSetup = lazy(() => import("./DesktopMountSetup.tsx"));

const Dialogs = () => {
  const showCreateArchive = useAppSelector((state) => state.globalState.createArchiveDialogOpen);
  const showExtractArchive = useAppSelector((state) => state.globalState.extractArchiveDialogOpen);
  const showRemoteDownload = useAppSelector((state) => state.globalState.remoteDownloadDialogOpen);
  const showAdvancedSearch = useAppSelector((state) => state.globalState.advanceSearchOpen);
  const showListViewColumnSetting = useAppSelector((state) => state.globalState.listViewColumnSettingDialogOpen);
  const directLink = useAppSelector((state) => state.globalState.directLinkDialogOpen);
  const excalidrawViewer = useAppSelector((state) => state.globalState.excalidrawViewer);
  const directLinkManagement = useAppSelector((state) => state.globalState.directLinkManagementDialogOpen);
  const archivePreview = useAppSelector((state) => state.globalState.archiveViewer);
  const desktopMountSetup = useAppSelector((state) => state.globalState.desktopMountSetupDialogOpen);

  return (
    <>
      <CreateNew />
      <DeleteConfirmation />
      <AggregatedErrorDetail />
      <LockConflictDetails />
      <Rename />
      <PathSelection />
      <Tags />
      <ChangeIcon />
      <ShareDialog />
      <VersionControl />
      <ManageShares />
      <StaleVersionConfirm />
      <SaveAs />
      <Photopea />
      <OpenWith />
      <Wopi />
      <CraftViewer />
      <CodeViewer />
      <DrawIOViewer />
      <MarkdownViewer />
      <VideoViewer />
      <PdfViewer />
      <CustomViewer />
      <EpubViewer />
      {showCreateArchive != undefined && (
        <Suspense fallback={null}>
          <CreateArchive />
        </Suspense>
      )}
      {showExtractArchive != undefined && (
        <Suspense fallback={null}>
          <ExtractArchive />
        </Suspense>
      )}
      {showRemoteDownload != undefined && (
        <Suspense fallback={null}>
          <CreateRemoteDownload />
        </Suspense>
      )}
      {showAdvancedSearch != undefined && (
        <Suspense fallback={null}>
          <AdvanceSearch />
        </Suspense>
      )}
      {showListViewColumnSetting != undefined && (
        <Suspense fallback={null}>
          <ColumnSetting />
        </Suspense>
      )}
      {directLink != undefined && <DirectLinks />}
      {excalidrawViewer != undefined && (
        <Suspense fallback={null}>
          <ExcalidrawViewer />
        </Suspense>
      )}
      {directLinkManagement != undefined && (
        <Suspense fallback={null}>
          <DirectLinksControl />
        </Suspense>
      )}
      {archivePreview != undefined && (
        <Suspense fallback={null}>
          <ArchivePreview />
        </Suspense>
      )}
      {desktopMountSetup != undefined && (
        <Suspense fallback={null}>
          <DesktopMountSetup />
        </Suspense>
      )}
    </>
  );
};

export default Dialogs;
