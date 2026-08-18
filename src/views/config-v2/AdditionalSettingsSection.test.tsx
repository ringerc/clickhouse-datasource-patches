import React from 'react';
import { render, fireEvent, screen } from '@testing-library/react';

import { AdditionalSettingsSection } from './AdditionalSettingsSection';
import { createTestProps } from './helpers';
import { CHCustomSetting, Protocol } from 'types/config';

/**
 * These tests document a v1 → v2 round-trip contract for `customSettings`.
 *
 * The v2 editor does not (yet) render UI for the enforcement fields
 * (`enforced`, `source`, `headerName`, `onMissing`, `jwt*`), but a datasource
 * provisioned or edited in v1 with those fields set must survive being edited
 * in v2. Concretely:
 *
 *   1. Editing the Setting or Value cell of an enforced row in v2 must NOT
 *      drop the enforcement metadata for that row.
 *   2. `onCustomSettingsChange`'s save filter must preserve header-sourced and
 *      JWT-sourced rows (whose `value` is intentionally empty), matching the
 *      v1 filter in CHConfigEditor.tsx.
 */
describe('AdditionalSettingsSection — custom settings round-trip', () => {
  const buildProps = (customSettings: CHCustomSetting[]) => {
    const onOptionsChange = jest.fn();
    const props = createTestProps({
      options: {
        jsonData: {
          host: 'localhost',
          port: 9000,
          protocol: Protocol.Native,
          username: '',
          customSettings,
        },
        secureJsonData: {},
        secureJsonFields: {},
      },
      mocks: { onOptionsChange },
    });
    return { props, onOptionsChange };
  };

  it('preserves enforced/source/headerName/onMissing when editing Setting in v2', () => {
    const initial: CHCustomSetting[] = [
      {
        setting: 'max_rows_to_read',
        value: '1000',
        enforced: true,
        source: 'static',
        onMissing: 'reject',
      },
    ];
    const { props, onOptionsChange } = buildProps(initial);

    render(<AdditionalSettingsSection {...props} />);

    const settingInput = screen.getByDisplayValue('max_rows_to_read');
    fireEvent.change(settingInput, { target: { value: 'max_rows_to_read_v2' } });
    fireEvent.blur(settingInput);

    expect(onOptionsChange).toHaveBeenCalled();
    const savedCustomSettings: CHCustomSetting[] =
      onOptionsChange.mock.calls[onOptionsChange.mock.calls.length - 1][0].jsonData.customSettings;

    expect(savedCustomSettings).toHaveLength(1);
    expect(savedCustomSettings[0]).toEqual({
      setting: 'max_rows_to_read_v2',
      value: '1000',
      enforced: true,
      source: 'static',
      onMissing: 'reject',
    });
  });

  it('preserves a header-sourced enforced row (empty value) across v2 save filter', () => {
    const initial: CHCustomSetting[] = [
      {
        setting: 'tenant_id',
        value: '',
        enforced: true,
        source: 'header',
        headerName: 'X-Tenant-Id',
        onMissing: 'reject',
      },
    ];
    const { props, onOptionsChange } = buildProps(initial);

    render(<AdditionalSettingsSection {...props} />);

    const settingInput = screen.getByDisplayValue('tenant_id');
    fireEvent.change(settingInput, { target: { value: 'tenant_id_v2' } });
    fireEvent.blur(settingInput);

    const savedCustomSettings: CHCustomSetting[] =
      onOptionsChange.mock.calls[onOptionsChange.mock.calls.length - 1][0].jsonData.customSettings;

    expect(savedCustomSettings).toHaveLength(1);
    expect(savedCustomSettings[0]).toEqual({
      setting: 'tenant_id_v2',
      value: '',
      enforced: true,
      source: 'header',
      headerName: 'X-Tenant-Id',
      onMissing: 'reject',
    });
  });

  it('preserves a JWT-sourced enforced row (empty value) across v2 save filter', () => {
    const initial: CHCustomSetting[] = [
      {
        setting: 'user_roles',
        value: '',
        enforced: true,
        source: 'jwt',
        onMissing: 'reject',
        jwtHeaderName: 'X-Id-Token',
        jwtClaimPath: ['realm_access', 'roles'],
        jwtClaimJoin: ',',
        jwtVerify: 'jwks',
        jwtJwksUrl: 'https://idp.example.com/jwks.json',
        jwtIssuer: 'https://idp.example.com/',
        jwtAudience: 'grafana',
      },
    ];
    const { props, onOptionsChange } = buildProps(initial);

    render(<AdditionalSettingsSection {...props} />);

    const settingInput = screen.getByDisplayValue('user_roles');
    fireEvent.change(settingInput, { target: { value: 'user_roles_v2' } });
    fireEvent.blur(settingInput);

    const savedCustomSettings: CHCustomSetting[] =
      onOptionsChange.mock.calls[onOptionsChange.mock.calls.length - 1][0].jsonData.customSettings;

    expect(savedCustomSettings).toHaveLength(1);
    expect(savedCustomSettings[0]).toEqual({
      setting: 'user_roles_v2',
      value: '',
      enforced: true,
      source: 'jwt',
      onMissing: 'reject',
      jwtHeaderName: 'X-Id-Token',
      jwtClaimPath: ['realm_access', 'roles'],
      jwtClaimJoin: ',',
      jwtVerify: 'jwks',
      jwtJwksUrl: 'https://idp.example.com/jwks.json',
      jwtIssuer: 'https://idp.example.com/',
      jwtAudience: 'grafana',
    });
  });

  it('disables the Value input for dynamic-source enforced rows to prevent silent overwrites', () => {
    const initial: CHCustomSetting[] = [
      {
        setting: 'tenant_id',
        value: '',
        enforced: true,
        source: 'header',
        headerName: 'X-Tenant-Id',
        onMissing: 'reject',
      },
    ];
    const { props } = buildProps(initial);

    render(<AdditionalSettingsSection {...props} />);

    const settingInput = screen.getByDisplayValue('tenant_id') as HTMLInputElement;
    const row = settingInput.closest('div[class*="css-"], .css-1qsu73n') ?? settingInput.parentElement?.parentElement;
    // Locate the Value input in the same row via its placeholder.
    const valueInput = screen.getByPlaceholderText(/Managed by v1 enforcement/i) as HTMLInputElement;
    expect(row).toBeTruthy();
    expect(valueInput.disabled).toBe(true);
  });

  it('still drops rows that are empty and non-enforced', () => {
    const initial: CHCustomSetting[] = [
      { setting: 'kept', value: 'v' },
      { setting: '', value: '' },
      { setting: 'dropped_no_value', value: '' },
    ];
    const { props, onOptionsChange } = buildProps(initial);

    render(<AdditionalSettingsSection {...props} />);

    // Trigger a save by editing the first row's value and blurring.
    const valueInput = screen.getByDisplayValue('v');
    fireEvent.change(valueInput, { target: { value: 'v2' } });
    fireEvent.blur(valueInput);

    const savedCustomSettings: CHCustomSetting[] =
      onOptionsChange.mock.calls[onOptionsChange.mock.calls.length - 1][0].jsonData.customSettings;

    expect(savedCustomSettings).toEqual([{ setting: 'kept', value: 'v2' }]);
  });
});
