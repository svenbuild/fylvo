# Fylvo UI style guide

The owner wants Fylvo to look and feel like the T3 Code desktop app: neutral near-black,
flat, quiet, and much more compact than stock Material UI. The Fylvo logo and the accent
color stay. This guide is the shared rulebook for every screen.

## Principles

1. **Compact.** A tool used daily, not a showcase. Small type, short rows, tight but even spacing.
2. **Flat.** Surfaces differ by a small step in lightness and a hairline border. No drop
   shadows on cards, no gradients, no glow, no ripple animations.
3. **Neutral.** Grays carry the interface. The accent appears only where it means something:
   the one primary action of a view, focus rings, checked controls, links, progress.
4. **Selection is gray, not accent.** Selected and hovered rows use neutral tints.
5. **Both modes.** Dark is the main mode, light must stay equally coherent. Use theme tokens,
   never hex values in components, so both follow automatically.

## Tokens

Colors come from the MUI theme (`inventory/fylvo_theme.json` plus `frontend/src/fylvoTheme.ts`).

| Role | Dark | How to get it |
| --- | --- | --- |
| Frame (sidebar, top bar) | `#000000` | `shellSurface(theme)` |
| Page | `#0A0A0A` | `theme.palette.background.paper` or `.default` |
| Card, field, quiet fill | `#141416` | `theme.palette.grey[900]` (light: `grey[100]`) |
| Hover | `#141416` | `hoverSurface(theme)` |
| Selected | `#1E1E22` | `selectedSurface(theme)` |
| Hover on a menu, popover or dialog | `#1E1E22` | `raisedHoverSurface(theme)` |
| Selected on a menu, popover or dialog | `#26262B` | `raisedSelectedSurface(theme)` |
| Hairline | white 9 % | `theme.palette.divider` |
| Text | `#F4F4F5` | `text.primary` |
| Quiet text | `#A9A9B0` | `text.secondary` |
| Accent | `#7484F4` | `primary.main` |

Menus, popovers and dialogs are already styled globally (raised surface, hairline, one soft shadow).
Their surface is `#141416` in dark mode, the same as the normal hover. Anything with a hover or
selected state on them uses the raised helpers, otherwise hover is invisible. List rows, table rows
and menu items get this from the theme already.

Buttons: the one primary action of a view is `contained` in the accent. Every other action is the
filled gray `SecondaryButton` (`variant="contained"`). Red outlined buttons are only for deleting.

## Sizes

| Thing | Target |
| --- | --- |
| Body text | 13 px (`body2`), meta text 12 px (`caption`) |
| Page or dialog title | 15 to 18 px, weight 600 |
| Section label | 12 px, weight 600, `text.secondary` |
| Navigation item | 28 px high |
| List or table row | 30 to 34 px high |
| Button, input, select | 30 to 32 px high |
| Icon | 16 to 18 px, never above 20 px inside controls |
| Radius | 8 px; chips and small tags 6 px; dialogs and menus 10 to 12 px |
| Gaps | 4, 6, 8, 12, 16 px. Page padding 12 to 16 px. Avoid anything above 24 px |
| Touch screens (`pointer: coarse` or below the `sm` breakpoint) | rows and buttons at least 40 px high |

`theme.spacing(1)` is 6 px. Many components already inherit the compact defaults
(small buttons, small inputs, dense menus). Fix what is still large by hand.

## Rules for changes

- Styling only. No behavior changes, no removed features, no renamed translation keys.
- Keep each change small and local (`sx`, `styled`). No new dependencies.
- Use the helpers `selectedSurface`, `hoverSurface`, `raisedHoverSurface`, `raisedSelectedSurface`,
  `shellSurface` from `frontend/src/fylvoTheme.ts`.
- Keyboard focus must stay visible.
- Every changed screen is checked with a screenshot in dark and light mode, on desktop
  (1536 x 1024) and on a phone (390 x 844).
