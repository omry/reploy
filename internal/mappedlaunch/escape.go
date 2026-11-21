package mappedlaunch

// CloneNamespaceMask is the native Linux clone argument-zero namespace mask
// shared by the mapped engine policy and the trusted startup boundary.
const CloneNamespaceMask uint32 = 0x7e020080
