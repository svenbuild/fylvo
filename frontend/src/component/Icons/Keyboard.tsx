import { SvgIcon, SvgIconProps } from "@mui/material";

export default function Keyboard(props: SvgIconProps) {
  return (
    <SvgIcon {...props}>
      <g fill="currentColor">
        <path d="M19.75 5A2.25 2.25 0 0 1 22 7.25v9.5A2.25 2.25 0 0 1 19.75 19H4.25A2.25 2.25 0 0 1 2 16.75v-9.5A2.25 2.25 0 0 1 4.25 5h15.5zm0 1.5H4.25a.75.75 0 0 0-.75.75v9.5c0 .41.34.75.75.75h15.5c.41 0 .75-.34.75-.75v-9.5a.75.75 0 0 0-.75-.75z" />
        <path d="M6 9h1.5v1.5H6zM9.25 9h1.5v1.5h-1.5zM12.5 9H14v1.5h-1.5zM15.75 9h1.5v1.5h-1.5zM6 12h1.5v1.5H6zM9.25 12h1.5v1.5h-1.5zM12.5 12H14v1.5h-1.5zM15.75 12h1.5v1.5h-1.5zM8 15h8v1.25H8z" />
      </g>
    </SvgIcon>
  );
}
