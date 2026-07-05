import React from 'react';
import Tabs from '@theme/Tabs';

const defaultPlatformValues = [
  {label: 'Linux', value: 'linux'},
  {label: 'Windows', value: 'windows'},
  {label: 'Mac', value: 'macos'},
];

export default function PlatformTabs({
  children,
  defaultValue = 'linux',
  groupId = 'reploy-platform',
  queryString = false,
  values = defaultPlatformValues,
}) {
  return (
    <div className="platform-tabs">
      <Tabs
        defaultValue={defaultValue}
        groupId={groupId}
        queryString={queryString}
        values={values}>
        {children}
      </Tabs>
    </div>
  );
}
