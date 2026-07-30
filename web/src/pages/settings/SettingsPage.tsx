import { Tabs, Typography } from 'antd'
import { FixedSettingsForm } from './FixedSettingsForm'
import { GitTokensSection } from './GitTokensSection'
import { AlertChannelsSection } from './AlertChannelsSection'
import { AdvancedSection } from './AdvancedSection'
import { FIXED_SECTIONS } from './registry'

// Settings presents the generic key/value store (internal/domain.Setting) as a
// traditional, category-per-tab settings screen: well-known keys become labeled
// fields, the variable-cardinality namespaces (git tokens, alert channels) get
// dedicated collection editors, and anything unmodeled falls through to Advanced.
export function SettingsPage() {
  const items = [
    ...FIXED_SECTIONS.map((section) => ({
      key: section.id,
      label: section.title,
      children: <FixedSettingsForm section={section} />,
    })),
    { key: 'git-tokens', label: 'Git tokens', children: <GitTokensSection /> },
    { key: 'alert-channels', label: 'Alert channels', children: <AlertChannelsSection /> },
    { key: 'advanced', label: 'Advanced', children: <AdvancedSection /> },
  ]

  return (
    <div>
      <div style={{ marginBottom: 20 }}>
        <Typography.Title level={3} className="agenda-display" style={{ margin: 0 }}>
          Settings
        </Typography.Title>
        <Typography.Text type="secondary">
          Runtime configuration — applied immediately, no restart. Secrets are encrypted at rest.
        </Typography.Text>
      </div>

      <Tabs tabPosition="left" defaultActiveKey={FIXED_SECTIONS[0]?.id} items={items} style={{ minHeight: 420 }} />
    </div>
  )
}
