import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Alert,
  App,
  Button,
  Checkbox,
  Drawer,
  Input,
  Space,
  Table,
  Tag,
  Tooltip,
  Typography,
  Upload,
} from 'antd'
import { InboxOutlined, SafetyCertificateOutlined } from '@ant-design/icons'
import type { UploadFile } from 'antd'
import * as api from '@/api/files'
import type { FileVerifyStatus, Machine, MachineFile } from '@/api/types'
import { errorMessage } from '@/utils/errorMessage'

const DEFAULT_MODE = '0600'

const VERIFY: Record<FileVerifyStatus, { color: string; label: string; hint: string }> = {
  ok: { color: 'green', label: 'OK', hint: 'Present, and its contents still hash to what was uploaded.' },
  changed: { color: 'orange', label: 'Changed', hint: 'Still there, but its contents differ from what was written.' },
  missing: { color: 'red', label: 'Missing', hint: 'The machine answered, and the file is not at that path.' },
  unreachable: {
    color: 'default',
    label: 'Unknown',
    hint: 'The machine could not be reached, so the file could not be checked.',
  },
  '': { color: 'blue', label: 'Not checked', hint: 'No verification has run for this record yet.' },
}

function formatTime(value: string | null): string {
  return value ? new Date(value).toLocaleString() : '—'
}

// MachineFilesDrawer uploads a file to a path the operator picks on one
// machine, and lists what has been uploaded there. Unlike an application's
// environment files, nothing mounts these anywhere — this is the escape hatch
// for files that are not part of an app's environment.
export function MachineFilesDrawer({
  machine,
  open,
  onClose,
}: {
  machine: Machine | null
  open: boolean
  onClose: () => void
}) {
  const { message } = App.useApp()
  const queryClient = useQueryClient()
  const [fileList, setFileList] = useState<UploadFile[]>([])
  const [targetPath, setTargetPath] = useState('')
  const [mode, setMode] = useState(DEFAULT_MODE)
  const [overwrite, setOverwrite] = useState(false)

  const machineId = machine?.id ?? 0
  // Every upload has to land inside the machine's workspace root. agenda-node
  // usually runs in a container with only that tree bind-mounted from the host,
  // so a write anywhere else goes into the node's own container: it reports
  // success, verifies as OK, and is gone at the next node restart. Presenting
  // the root as a fixed prefix means the path cannot be wrong to begin with.
  const root = (machine?.effective_workspace_root ?? '').replace(/\/+$/, '')
  const relative = targetPath.replace(/^\/+/, '')
  const absolutePath = relative ? `${root}/${relative}` : ''
  const relativeError = relative.split('/').includes('..')
    ? "Path segments may not be '..'."
    : null
  const queryKey = ['machines', machineId, 'files']
  const { data, isLoading } = useQuery({
    queryKey,
    queryFn: () => api.listMachineFiles(machineId),
    enabled: open && machineId > 0,
  })

  const selected = fileList[0]?.originFileObj as File | undefined

  const uploadMutation = useMutation({
    mutationFn: () =>
      api.uploadMachineFile(machineId, { path: absolutePath, file: selected as File, mode, overwrite }),
    onSuccess: (rec) => {
      message.success(`Uploaded to ${rec.path}`)
      setFileList([])
      setTargetPath('')
      setOverwrite(false)
      queryClient.invalidateQueries({ queryKey })
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  const verifyMutation = useMutation({
    mutationFn: (fileId: number) => api.verifyMachineFile(fileId),
    onSuccess: (file) => {
      const v = VERIFY[file.last_verify_status]
      if (file.last_verify_status === 'ok') message.success(`${file.path}: ${v.label}`)
      else message.warning(`${file.path}: ${v.label}${file.last_verify_error ? ` — ${file.last_verify_error}` : ''}`)
      queryClient.invalidateQueries({ queryKey })
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  return (
    <Drawer
      title={machine ? `Files on ${machine.name}` : 'Files'}
      open={open}
      onClose={onClose}
      width={880}
      destroyOnHidden
    >
      <Space direction="vertical" size="middle" style={{ width: '100%' }}>
        <Alert
          type="info"
          showIcon
          message="These files are not mounted into any container."
          description="To give an application a credential, use the Files tab on the application instead — those are delivered to every machine in the environment and mounted at /agenda/files. Use this page for files that are not part of an app's environment."
        />

        <Upload.Dragger
          maxCount={1}
          fileList={fileList}
          beforeUpload={() => false}
          onChange={({ fileList: next }) => setFileList(next.slice(-1))}
        >
          <p className="ant-upload-drag-icon">
            <InboxOutlined />
          </p>
          <p className="ant-upload-text">Click or drag a file here</p>
        </Upload.Dragger>

        {root ? (
          <>
            <Input
              className="agenda-mono"
              addonBefore={`${root}/`}
              value={relative}
              status={relativeError ? 'error' : undefined}
              placeholder="shared/ca.pem"
              onChange={(e) => setTargetPath(e.target.value)}
            />
            <Typography.Text type={relativeError ? 'danger' : 'secondary'}>
              {relativeError ??
                "Relative to the machine's workspace root. Uploads outside it are refused: a containerized agenda-node would write them inside its own container, where they look fine and disappear on restart."}
            </Typography.Text>
          </>
        ) : (
          <Alert
            type="error"
            showIcon
            message="This machine has no workspace root"
            description="Set workspace_root on the machine (or globally in agenda-v2.yaml) before uploading — without it there is no directory agenda is entitled to write to here."
          />
        )}

        <Space>
          <Input
            className="agenda-mono"
            addonBefore="Mode"
            style={{ width: 200 }}
            value={mode}
            placeholder={DEFAULT_MODE}
            onChange={(e) => setMode(e.target.value)}
          />
          <Checkbox checked={overwrite} onChange={(e) => setOverwrite(e.target.checked)}>
            Replace if it already exists
          </Checkbox>
          <Button
            type="primary"
            disabled={!selected || !relative || !root || relativeError !== null}
            loading={uploadMutation.isPending}
            onClick={() => uploadMutation.mutate()}
          >
            Upload
          </Button>
        </Space>

        <Table<MachineFile>
          rowKey="id"
          size="small"
          loading={isLoading}
          dataSource={data?.data ?? []}
          pagination={false}
          locale={{ emptyText: 'Nothing has been uploaded to this machine yet.' }}
          columns={[
            {
              title: 'Path',
              dataIndex: 'path',
              render: (path: string, row: MachineFile) => (
                <Space direction="vertical" size={0}>
                  <Typography.Text className="agenda-mono" strong={row.current}>
                    {path}
                  </Typography.Text>
                  {!row.current && <Tag>superseded</Tag>}
                  {row.scope === 'app_env' && <Tag color="geekblue">{`${row.app_name} / ${row.env}`}</Tag>}
                </Space>
              ),
            },
            {
              title: 'State',
              dataIndex: 'last_verify_status',
              width: 120,
              render: (status: FileVerifyStatus, row: MachineFile) => {
                const v = VERIFY[status] ?? VERIFY['']
                return (
                  <Tooltip title={row.last_verify_error || v.hint}>
                    <Tag color={v.color}>{v.label}</Tag>
                  </Tooltip>
                )
              },
            },
            {
              title: 'Checked',
              dataIndex: 'last_verified_at',
              width: 170,
              render: (value: string | null) => (
                <Typography.Text type="secondary">{formatTime(value)}</Typography.Text>
              ),
            },
            {
              title: 'Uploaded',
              dataIndex: 'created_at',
              width: 200,
              render: (value: string, row: MachineFile) => (
                <Typography.Text type="secondary">
                  {formatTime(value)} by {row.username || 'unknown'}
                </Typography.Text>
              ),
            },
            {
              title: '',
              key: 'actions',
              width: 100,
              render: (_: unknown, row: MachineFile) => (
                <Button
                  size="small"
                  icon={<SafetyCertificateOutlined />}
                  loading={verifyMutation.isPending && verifyMutation.variables === row.id}
                  onClick={() => verifyMutation.mutate(row.id)}
                >
                  Check
                </Button>
              ),
            },
          ]}
        />
      </Space>
    </Drawer>
  )
}
