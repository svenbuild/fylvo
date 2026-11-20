import { getFileActivity } from "../../../api/api.ts";
import { FileResponse } from "../../../api/explorer.ts";
import { useAppDispatch } from "../../../redux/hooks.ts";
import ActivityList from "../../Common/Activity/ActivityList.tsx";
import { useActivityFeed } from "../../Common/Activity/useActivityFeed.ts";

// Fylvo: the history of a file or folder, only for its owner (see hasFileActivity).
const FileActivity = ({ target }: { target: FileResponse }) => {
  const dispatch = useAppDispatch();
  const feed = useActivityFeed(target.path, (next) => dispatch(getFileActivity(target.path, next)));
  return <ActivityList {...feed} onLoadMore={feed.loadMore} />;
};

// hasFileActivity tells whether the current user may see the history of target: the owner,
// through the own files or the trash. The server checks the same.
export const hasFileActivity = (target: FileResponse) =>
  !!target.owned && (target.path.startsWith("cloudreve://my") || target.path.startsWith("cloudreve://trash"));

export default FileActivity;
