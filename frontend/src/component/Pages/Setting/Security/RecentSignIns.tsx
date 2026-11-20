import { getOwnActivity } from "../../../../api/api.ts";
import { useAppDispatch } from "../../../../redux/hooks.ts";
import ActivityList from "../../../Common/Activity/ActivityList.tsx";
import { useActivityFeed } from "../../../Common/Activity/useActivityFeed.ts";

// Fylvo: sign-ins and security changes of the own account, with address and device.
const RecentSignIns = () => {
  const dispatch = useAppDispatch();
  const feed = useActivityFeed("own", (next) => dispatch(getOwnActivity(next)));
  return <ActivityList {...feed} withDevice onLoadMore={feed.loadMore} />;
};

export default RecentSignIns;
