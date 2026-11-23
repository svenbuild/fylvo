import { ListViewColumnSetting } from "../component/FileManager/Explorer/ListView/Column.tsx";
import { User } from "./user.ts";

export interface PaginationArgs {
  page?: number;
  page_size?: number;
  order_by?: string;
  order_direction?: string;
  next_page_token?: string;
}

export interface ListFileService extends PaginationArgs {
  uri: string;
  // Fylvo: everything below the folder; a search in a connected storage needs it.
  recursive?: boolean;
}

export const FileType = {
  file: 0,
  folder: 1,
};

export interface FileResponse {
  type: number;
  id: string;
  name: string;
  created_at: string;
  updated_at: string;
  size: number;
  metadata?: {
    [key: string]: string;
  };
  path: string;
  shared?: boolean;
  capability?: string;
  owned?: boolean;
  folder_summary?: FolderSummary;
  extended_info?: ExtendedInfo;
  primary_entity?: string;
}

export interface FolderSummary {
  size: number;
  files: number;
  folders: number;
  completed: boolean;
  calculated_at: string;
}

export interface ExtendedInfo {
  storage_policy?: StoragePolicy;
  storage_used: number;
  shares?: Share[];
  entities?: Entity[];
  view?: ExplorerView;
  direct_links?: DirectLink[];
}

export interface DirectLink {
  id: string;
  created_at: string;
  url: string;
  downloaded: number;
}

export interface Entity {
  id: string;
  type: number;
  created_at: string;
  storage_policy?: StoragePolicy;
  size: number;
  created_by?: User;
  encrypted_with?: EncryptionCipher;
}

export interface Share {
  id: string;
  name?: string;
  expires?: string;
  is_private?: boolean;
  share_view?: boolean;
  remain_downloads?: number;
  created_at?: string;
  url: string;
  visited: number;
  downloaded: number;
  expired?: boolean;
  unlocked: boolean;
  password_protected: boolean;
  source_type?: number;
  owner: User;
  source_uri?: string;
  password?: string;
  show_readme?: boolean;
  // Fylvo: member share, only returned to the share owner.
  member_only?: boolean;
  members?: ShareMember[];
  // Fylvo: link of a folder that accepts uploads; hide_content makes it a drop box ("upload only").
  allow_upload?: boolean;
  hide_content?: boolean;
  // Fylvo: owner only, in bytes. upload_limit 0 or missing means no limit.
  upload_limit?: number;
  uploaded?: number;
}

// Fylvo: a registered user a folder is shared with. user_id is the hashed user ID.
export interface ShareMember {
  user_id: string;
  can_edit?: boolean;
}

export enum PolicyType {
  local = "local",
  remote = "remote",
  oss = "oss",
  qiniu = "qiniu",
  onedrive = "onedrive",
  cos = "cos",
  upyun = "upyun",
  s3 = "s3",
  ks3 = "ks3",
  obs = "obs",
  load_balance = "load_balance",
}

export interface StoragePolicy {
  id: string;
  name: string;
  allowed_suffix?: string[];
  denied_suffix?: string[];
  allowed_name_regexp?: string;
  denied_name_regexp?: string;
  max_size: number;
  type: PolicyType;
  relay?: boolean;
  chunk_concurrency?: number;
  encryption?: boolean;
  streaming_encryption?: boolean;
}

export interface PaginationResults {
  page: number;
  page_size: number;
  total_items?: number;
  next_token?: string;
  is_cursor?: boolean;
}

export interface NavigatorProps {
  capability?: string;
  max_page_size: number;
  order_by_options: string[];
  order_direction_options: string[];
}

export interface ExplorerView {
  page_size: number;
  order?: string;
  order_direction?: string;
  view?: string;
  thumbnail?: boolean;
  columns?: ListViewColumnSetting[];
  gallery_width?: number;
}

export interface ListResponse {
  files: FileResponse[];
  pagination: PaginationResults;
  props: NavigatorProps;
  context_hint?: string;
  recursion_limit_reached?: boolean;
  mixed_type?: boolean;
  single_file_view?: boolean;
  parent?: FileResponse;
  storage_policy?: StoragePolicy;
  view?: ExplorerView;
  // Fylvo: set when the folder is in a connected storage.
  connected?: ConnectedListInfo;
}

// Fylvo: what a listing of a connected storage tells about it.
export interface ConnectedListInfo {
  id: string;
  provider: string;
  read_only: boolean;
  search: boolean;
  thumbnails: boolean;
  // Entries whose names cannot be shown here.
  omitted?: number;
  // A walk through the folders (a search) did not see everything.
  incomplete?: boolean;
  // Shortcuts that were listed but not entered by such a walk.
  links?: number;
}

export const Metadata = {
  share_redirect: "sys:shared_redirect",
  share_owner: "sys:shared_owner",
  upload_session_id: "sys:upload_session_id",
  icon_color: "customize:icon_color",
  emoji: "customize:emoji",
  live_photo: "customize:live_photo",
  tag_prefix: "tag:",
  thumbDisabled: "thumb:disabled",
  restore_uri: "sys:restore_uri",
  expected_collect_time: "sys:expected_collect_time",
  full_text_index: "sys:fulltext_index",

  // Fylvo: rows of a connected storage
  // A name that exists more than once in the folder: shown, but nothing can be done with it.
  connected_unsupported: "fylvo:unsupported",
  // A shortcut at the provider: read only.
  connected_link: "fylvo:link",
  // A document of the provider (e.g. Google Sheets): downloads in this format.
  connected_export: "fylvo:export",
  // Every format a provider's document downloads in, the usual one first (comma separated).
  connected_exports: "fylvo:exports",
  // Address of the item at the provider, to open it there.
  connected_external_url: "fylvo:external_url",
  // The size is not known; do not show it.
  connected_size_unknown: "fylvo:size_unknown",

  // Exif
  gps_lng: "exif:longitude",
  gps_lat: "exif:latitude",
  gps_attitude: "exif:altitude",
  artist: "exif:artist",
  copy_right: "exif:copy_right",
  camera_model: "exif:camera_model",
  camera_make: "exif:camera_make",
  camera_owner_name: "exif:camera_owner_name",
  body_serial_number: "exif:body_serial_number",
  lens_make: "exif:lens_make",
  lens_model: "exif:lens_model",
  software: "exif:software",
  exposure_time: "exif:exposure_time",
  f_number: "exif:f",
  aperture_value: "exif:aperture_value",
  focal_length: "exif:focal_length",
  iso_speed_ratings: "exif:iso",
  pixel_x_dimension: "exif:x",
  pixel_y_dimension: "exif:y",
  orientation: "exif:orientation",
  taken_at: "exif:taken_at",
  flash: "exif:flash",
  image_description: "exif:image_description",
  projection_type: "exif:projection_type",
  exposure_bias_value: "exif:exposure_bias",

  // Music
  music_title: "music:title",
  music_artist: "music:artist",
  music_album: "music:album",

  // Stream
  stream_title: "stream:title",
  stream_duration: "stream:duration",
  stream_format_name: "stream:format",
  stream_format_long: "stream:formatLong",
  stream_bit_rate: "stream:bitrate",
  stream_description: "stream:description",
  stream_indexed_codec: "codec",
  stream_indexed_bitrate: "bitrate",
  stream_indexed_width: "width",
  stream_indexed_height: "height",

  // Geocoding
  street: "geocoding:street",
  locality: "geocoding:locality",
  place: "geocoding:place",
  district: "geocoding:district",
  region: "geocoding:region",
  country: "geocoding:country",
};

export interface FileThumbResponse {
  url: string;
  expires?: string;
}

export interface DeleteFileService {
  uris: string[];
  unlink?: boolean;
  skip_soft_delete?: boolean;
  // Fylvo: in a connected storage, the id of each listed row by the address sent for it
  // (rename, move and delete act on exactly the row the user saw).
  ids?: { [uri: string]: string };
}

export interface LockApplication {
  type: string;
  inner_xml?: string;
  viewer_id?: string;
}

export interface LockOwner {
  application: LockApplication;
}

export interface ConflictDetail {
  path?: string;
  token?: string;
  owner?: LockOwner;
  type: number;
}

export interface UnlockFileService {
  tokens: string[];
}

export interface RenameFileService {
  uri: string;
  new_name: string;
  // Fylvo: in a connected storage, the id of each listed row by the address sent for it
  // (rename, move and delete act on exactly the row the user saw).
  ids?: { [uri: string]: string };
}

export const NavigatorCapability = {
  create_file: 0,
  rename_file: 1,
  upload_file: 6,
  download_file: 7,
  update_metadata: 8,
  list_children: 9,
  generate_thumb: 10,
  delete_file: 14,
  lock_file: 15,
  soft_delete: 16,
  restore: 17,
  share: 18,
  info: 19,
  version_control: 20,
  enter_folder: 23,
};

export interface PinFileService {
  uri: string;
  name?: string;
}

export interface MoveFileService extends MultipleUriService {
  dst: string;
  copy?: boolean;
  // Fylvo: in a connected storage, the id of each listed row by the address sent for it
  // (rename, move and delete act on exactly the row the user saw).
  ids?: { [uri: string]: string };
}

export interface MetadataPatch {
  key: string;
  value?: string;
  remove?: boolean;
}

export interface PatchMetadataService extends MultipleUriService {
  patches: MetadataPatch[];
}

export interface ShareCreateService {
  uri: string;
  downloads?: number;
  is_private?: boolean;
  password?: string;
  expire?: number;
  share_view?: boolean;
  show_readme?: boolean;
  member_only?: boolean;
  members?: ShareMember[];
  allow_upload?: boolean;
  hide_content?: boolean;
  upload_limit?: number;
}

export interface CreateFileService {
  uri: string;
  type: "file" | "folder";
  err_on_conflict?: boolean;
  metadata?: {
    [key: string]: string;
  };
}

export interface FileURLService extends MultipleUriService {
  download?: boolean;
  redirect?: boolean;
  entity?: string;
  no_cache?: boolean;
  skip_error?: boolean;
  use_primary_site_url?: boolean;
  archive?: boolean;
  // Fylvo: the format a provider's document of a connected storage is downloaded in.
  export?: string;
}

export interface FileURLResponse {
  urls: EntityURLResponse[];
  expires: string;
}

export interface EntityURLResponse {
  url: string;
  stream_saver_display_name?: string;
}

export interface GetFileInfoService {
  uri?: string;
  id?: string;
  extended?: boolean;
  folder_summary?: boolean;
}

export enum EntityType {
  version = 0,
  thumbnail = 1,
  live_photo = 2,
}

export interface VersionControlService {
  uri: string;
  version: string;
}

export interface MultipleUriService {
  uris: string[];
}

export const ViewerAction = {
  edit: "edit",
  view: "view",
};

export const ViewerType = {
  builtin: "builtin",
  wopi: "wopi",
  custom: "custom",
};

export enum ViewerPlatform {
  pc = "pc",
  mobile = "mobile",
  all = "all",
}

export interface Viewer {
  id: string;
  type: string;
  display_name: string;
  exts: string[];
  icon: string;
  url?: string;
  max_size?: number;
  disabled?: boolean;
  props?: {
    [key: string]: string;
  };
  wopi_actions?: {
    [key: string]: {
      [key: string]: string;
    };
  };
  templates?: NewFileTemplate[];
  platform?: ViewerPlatform;
  required_group_permission?: number[];
}

export interface NewFileTemplate {
  ext: string;
  display_name: string;
}

export interface ViewerGroup {
  viewers: Viewer[];
}

export interface FileUpdateService {
  uri: string;
  previous?: string;
  // Fylvo: refuse (40904) instead of creating the file when it is gone.
  previous_required?: boolean;
}

export interface ViewerSession {
  id: string;
  access_token: string;
  expires: number;
}

export interface ViewerSessionResponse {
  session: ViewerSession;
  wopi_src?: string;
  // Fylvo: only for a file of a connected storage: whether the session may save.
  edit?: boolean;
}

export interface CreateViewerSessionService {
  uri: string;
  viewer_id: string;
  preferred_action: string;
  version?: string;
}

export enum EncryptionCipher {
  aes256ctr = "aes-256-ctr",
}

export interface UploadSessionRequest {
  uri: string;
  size: number;
  policy_id: string;
  last_modified?: number;
  entity_type?: string;
  metadata?: {
    [key: string]: string;
  };
  mime_type?: string;
  encryption_supported?: EncryptionCipher[];
}

export interface EncryptMetadata {
  algorithm: EncryptionCipher;
  key_plain_text: string;
  iv: string;
}

export interface UploadCredential {
  session_id: string;
  expires: number;
  chunk_size: number;
  upload_urls: string[];
  credential: string;
  uploadID: string;
  callback: string;
  ak: string;
  keyTime: string;
  path: string;
  completeURL: string;
  storage_policy?: StoragePolicy;
  uri: string;
  callback_secret: string;
  mime_type?: string;
  upload_policy?: string;
  encrypt_metadata?: EncryptMetadata;
}

export interface DeleteUploadSessionService {
  id: string;
  uri: string;
}

export interface DirectLink {
  file_url: string;
  link: string;
}

export interface PatchViewSyncService {
  uri: string;
  view?: ExplorerView;
}

export interface CustomProps {
  id: string;
  name: string;
  type: CustomPropsType;
  max?: number;
  min?: number;
  default?: string;
  options?: string[];
  icon?: string;
}

export enum CustomPropsType {
  text = "text",
  number = "number",
  boolean = "boolean",
  select = "select",
  multi_select = "multi_select",
  user = "user",
  link = "link",
  rating = "rating",
}

export interface ArchivedFile {
  name: string;
  size: number;
  updated_at?: string;
  is_directory: boolean;
}

export interface ArchiveListFilesResponse {
  files: ArchivedFile[];
}

export interface ArchiveListFilesService {
  uri: string;
  entity?: string;
  text_encoding?: string;
}

export interface FullTextSearchResults {
  hits?: FullTextSearchResult[];
  total: number;
}

export interface FullTextSearchResult {
  file: FileResponse;
  content: string;
}
