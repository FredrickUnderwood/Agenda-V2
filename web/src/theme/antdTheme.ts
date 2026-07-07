import type { ThemeConfig } from 'antd'
import { color, font, radius } from './tokens'

export const antdTheme: ThemeConfig = {
  token: {
    colorPrimary: color.signal,
    colorSuccess: color.verified,
    colorError: color.fail,
    colorInfo: color.wire,
    colorLink: color.wire,
    colorBgLayout: color.paper,
    colorBgContainer: color.paperRaised,
    colorBgElevated: color.paperRaised,
    colorBorder: color.paperBorder,
    colorBorderSecondary: color.paperBorder,
    colorText: color.ink900,
    colorTextSecondary: color.ink500,
    colorTextTertiary: color.ink500,
    fontFamily: font.body,
    borderRadius: radius.md,
    borderRadiusSM: radius.sm,
    borderRadiusLG: radius.lg,
  },
  components: {
    Layout: {
      siderBg: color.ink,
      headerBg: color.paperRaised,
      bodyBg: color.paper,
    },
    Menu: {
      darkItemBg: color.ink,
      darkItemSelectedBg: color.inkRaised,
      darkItemColor: '#C7C9D1',
      darkItemHoverColor: '#FFFFFF',
      darkItemSelectedColor: color.signal,
    },
    Card: {
      colorBorderSecondary: color.paperBorder,
    },
    Table: {
      headerBg: '#EEECE5',
      headerColor: color.ink500,
      borderColor: color.paperBorder,
    },
    Button: {
      primaryShadow: 'none',
      fontWeight: 500,
    },
  },
}
