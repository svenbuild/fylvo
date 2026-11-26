export interface Condition {
  type: ConditionType;
  case_folding?: boolean;
  names?: string[];
  name_op_or?: boolean;
  file_type?: number;
  size_gte?: number;
  size_lte?: number;
  time?: number;
  metadata_key?: string;
  metadata_value?: string;
  metadata_strong_match?: boolean;
  base_uri?: string;
  tags?: string[];
  id?: string;
  metadata_key_readonly?: boolean;
  created_gte?: number;
  created_lte?: number;
  updated_gte?: number;
  updated_lte?: number;
}

export enum ConditionType {
  name,
  size,
  created,
  modified,
  type,
  metadata,
  base,
  tag,
}
